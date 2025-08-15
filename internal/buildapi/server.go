package buildapi

import (
	"archive/tar"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	kscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automotivev1 "github.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
	authnv1 "k8s.io/api/authentication/v1"
)

type APIServer struct {
	server *http.Server
	addr   string
	log    logr.Logger
}

//go:embed openapi.yaml
var embeddedOpenAPI []byte

type ctxKeyReqID struct{}

// NewAPIServer creates a new API server runnable
func NewAPIServer(addr string) *APIServer {
	a := &APIServer{addr: addr}
	a.log = logr.Discard()
	a.server = &http.Server{Addr: addr, Handler: a.createHandler()}
	return a
}

func NewAPIServerWithLogger(addr string, logger logr.Logger) *APIServer {
	a := &APIServer{addr: addr, log: logger}
	a.server = &http.Server{Addr: addr, Handler: a.createHandler()}
	return a
}

// Start implements manager.Runnable
func (a *APIServer) Start(ctx context.Context) error {

	go func() {
		a.log.Info("build-api listening", "addr", a.addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.log.Error(err, "build-api server error")
		}
	}()

	<-ctx.Done()
	a.log.Info("shutting down build-api server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.log.Error(err, "build-api server forced to shutdown")
		return err
	}
	a.log.Info("build-api server exited")
	return nil
}

func (a *APIServer) createHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(embeddedOpenAPI)
	})
	mux.HandleFunc("/v1/builds", a.handleBuilds)
	mux.HandleFunc("/v1/builds/", a.handleBuildByName)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// attach a request ID for correlation
		reqID := uuid.New().String()
		ctx := context.WithValue(r.Context(), ctxKeyReqID{}, reqID)
		a.log.Info("http request", "method", r.Method, "path", r.URL.Path, "reqID", reqID)
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

// StartServer starts the REST API server on the given address in a goroutine and returns the server
func StartServer(addr string) (*http.Server, error) {
	api := NewAPIServer(addr)
	server := api.server
	go func() {
		if err := api.Start(context.Background()); err != nil {
			// no logger available here
		}
	}()
	return server, nil
}

func (a *APIServer) handleBuilds(w http.ResponseWriter, r *http.Request) {
	// Validate authentication
	if !isAuthenticated(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodPost:
		a.log.Info("create build")
		createBuild(w, r)
	case http.MethodGet:
		a.log.Info("list builds")
		listBuilds(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *APIServer) handleBuildByName(w http.ResponseWriter, r *http.Request) {
	// Validate authentication (except for healthz)
	parts := splitPath(r.URL.Path)
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	name := parts[2]

	// Skip auth for healthz endpoint
	if !(len(parts) == 2 && parts[1] == "healthz") {
		if !isAuthenticated(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		if len(parts) == 4 {
			switch parts[3] {
			case "logs":
				a.log.Info("logs requested", "build", name, "reqID", r.Context().Value(ctxKeyReqID{}))
				streamLogs(w, r, name)
				return
			case "artifact":
				a.log.Info("artifact requested", "build", name, "reqID", r.Context().Value(ctxKeyReqID{}))
				streamArtifact(w, r, name)
				return
			case "template":
				a.log.Info("template requested", "build", name, "reqID", r.Context().Value(ctxKeyReqID{}))
				getBuildTemplate(w, r, name)
				return
			}
		}
		a.log.Info("get build", "build", name, "reqID", r.Context().Value(ctxKeyReqID{}))
		getBuild(w, r, name)
	case http.MethodPost:
		if len(parts) == 4 && parts[3] == "uploads" {
			a.log.Info("uploads", "build", name, "reqID", r.Context().Value(ctxKeyReqID{}))
			uploadFiles(w, r, name)
			return
		}
		http.NotFound(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func streamLogs(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	var podName string

	ib := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ib); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tr := strings.TrimSpace(ib.Status.TaskRunName)
	if tr == "" {
		http.Error(w, "logs not available yet", http.StatusServiceUnavailable)
		return
	}
	restCfg, err := getRESTConfigFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	quickCS, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pods, err := quickCS.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "tekton.dev/taskRun=" + tr})
	if err != nil || len(pods.Items) == 0 {
		http.Error(w, "logs not available yet", http.StatusServiceUnavailable)
		return
	}
	podName = pods.Items[0].Name

	cfg, err := getRESTConfigFromRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Waiting for logs...\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	var hadStream bool
	streamed := make(map[string]bool)
	var lastErrs []string
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		stepNames := make([]string, 0, len(pod.Spec.Containers))
		for _, c := range pod.Spec.Containers {
			if strings.HasPrefix(c.Name, "step-") {
				stepNames = append(stepNames, c.Name)
			}
		}
		if len(stepNames) == 0 {
			for _, c := range pod.Spec.Containers {
				stepNames = append(stepNames, c.Name)
			}
		}

		var errs []string

		for _, cName := range stepNames {
			if streamed[cName] {
				continue
			}

			req := cs.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{Container: cName, Follow: true})
			stream, err := req.Stream(ctx)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", cName, err))
				continue
			}

			if !hadStream {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			hadStream = true

			_, _ = w.Write([]byte("\n===== Logs from " + strings.TrimPrefix(cName, "step-") + " =====\n\n"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			io.Copy(w, stream)
			stream.Close()
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}

			streamed[cName] = true
		}

		if len(errs) > 0 {
			lastErrs = errs
		}

		if len(streamed) == len(stepNames) {
			break
		}
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			break
		}

		time.Sleep(2 * time.Second)
		if !hadStream {
			// keep-alive to prevent router/proxy 504s while waiting
			_, _ = w.Write([]byte("."))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}

	if !hadStream {
		http.Error(w, "logs unavailable: "+strings.Join(lastErrs, "; "), http.StatusServiceUnavailable)
		return
	}
}

func splitPath(p string) []string {
	if len(p) > 0 && p[0] == '/' {
		p = p[1:]
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return []string{}
	}
	raw := strings.Split(p, "/")
	parts := make([]string, 0, len(raw))
	for _, s := range raw {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

func createBuild(w http.ResponseWriter, r *http.Request) {
	var req BuildRequest
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	needsUpload := strings.Contains(req.Manifest, "source_path")

	if req.Name == "" || req.Manifest == "" {
		http.Error(w, "name and manifest are required", http.StatusBadRequest)
		return
	}

	if req.Distro == "" {
		req.Distro = "cs9"
	}
	if req.Target == "" {
		req.Target = "qemu"
	}
	if req.Architecture == "" {
		req.Architecture = "arm64"
	}
	if req.ExportFormat == "" {
		req.ExportFormat = "image"
	}
	if req.Mode == "" {
		req.Mode = "image"
	}

	if !req.Distro.IsValid() {
		http.Error(w, "distro cannot be empty", http.StatusBadRequest)
		return
	}
	if !req.Target.IsValid() {
		http.Error(w, "target cannot be empty", http.StatusBadRequest)
		return
	}
	if !req.Architecture.IsValid() {
		http.Error(w, "architecture cannot be empty", http.StatusBadRequest)
		return
	}
	if !req.ExportFormat.IsValid() {
		http.Error(w, "exportFormat cannot be empty", http.StatusBadRequest)
		return
	}
	if !req.Mode.IsValid() {
		http.Error(w, "mode cannot be empty", http.StatusBadRequest)
		return
	}
	if req.AutomotiveImageBuilder == "" {
		req.AutomotiveImageBuilder = "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0"
	}
	if req.ManifestFileName == "" {
		req.ManifestFileName = "manifest.aib.yml"
	}

	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("k8s client error: %v", err), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	namespace := resolveNamespace()

	existing := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: namespace}, existing); err == nil {
		http.Error(w, fmt.Sprintf("ImageBuild %s already exists", req.Name), http.StatusConflict)
		return
	} else if !k8serrors.IsNotFound(err) {
		http.Error(w, fmt.Sprintf("error checking existing build: %v", err), http.StatusInternalServerError)
		return
	}

	cfgName := fmt.Sprintf("%s-manifest", req.Name)
	cmData := map[string]string{req.ManifestFileName: req.Manifest}

	if len(req.CustomDefs) > 0 {
		cmData["custom-definitions.env"] = strings.Join(req.CustomDefs, "\n")
	}
	if len(req.AIBOverrideArgs) > 0 {
		// If override is provided, prefer it and ignore the regular extra args
		cmData["aib-override-args.txt"] = strings.Join(req.AIBOverrideArgs, " ")
	} else if len(req.AIBExtraArgs) > 0 {
		cmData["aib-extra-args.txt"] = strings.Join(req.AIBExtraArgs, " ")
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfgName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                  "build-api",
				"app.kubernetes.io/part-of":                     "automotive-dev",
				"app.kubernetes.io/created-by":                  "automotive-dev-build-api",
				"automotive.sdv.cloud.redhat.com/resource-type": "manifest-config",
			},
		},
		Data: cmData,
	}
	if err := k8sClient.Create(ctx, cm); err != nil {
		http.Error(w, fmt.Sprintf("error creating manifest ConfigMap: %v", err), http.StatusInternalServerError)
		return
	}

	labels := map[string]string{
		"app.kubernetes.io/managed-by":                 "build-api",
		"app.kubernetes.io/part-of":                    "automotive-dev",
		"app.kubernetes.io/created-by":                 "automotive-dev-build-api",
		"automotive.sdv.cloud.redhat.com/distro":       string(req.Distro),
		"automotive.sdv.cloud.redhat.com/target":       string(req.Target),
		"automotive.sdv.cloud.redhat.com/architecture": string(req.Architecture),
	}

	serveExpiryHours := int32(24)
	{
		autoDev := &automotivev1.AutomotiveDev{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "automotive-dev", Namespace: namespace}, autoDev); err == nil {
			if autoDev.Spec.BuildConfig != nil && autoDev.Spec.BuildConfig.ServeExpiryHours > 0 {
				serveExpiryHours = autoDev.Spec.BuildConfig.ServeExpiryHours
			}
		}
	}

	imageBuild := &automotivev1.ImageBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: automotivev1.ImageBuildSpec{
			Distro:                 string(req.Distro),
			Target:                 string(req.Target),
			Architecture:           string(req.Architecture),
			ExportFormat:           string(req.ExportFormat),
			Mode:                   string(req.Mode),
			AutomotiveImageBuilder: req.AutomotiveImageBuilder,
			ServeArtifact:          req.ServeArtifact,
			ServeExpiryHours:       serveExpiryHours,
			ManifestConfigMap:      cfgName,
			InputFilesServer:       needsUpload,
		},
	}
	if err := k8sClient.Create(ctx, imageBuild); err != nil {
		http.Error(w, fmt.Sprintf("error creating ImageBuild: %v", err), http.StatusInternalServerError)
		return
	}

	if err := setOwnerRef(ctx, k8sClient, namespace, cfgName, imageBuild); err != nil {
		// best-effort
	}

	writeJSON(w, http.StatusAccepted, BuildResponse{
		Name:    req.Name,
		Phase:   "Building",
		Message: "Build triggered",
	})
}

func listBuilds(w http.ResponseWriter, r *http.Request) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("k8s client error: %v", err), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	list := &automotivev1.ImageBuildList{}
	if err := k8sClient.List(ctx, list, client.InNamespace(namespace)); err != nil {
		http.Error(w, fmt.Sprintf("error listing builds: %v", err), http.StatusInternalServerError)
		return
	}

	resp := make([]BuildListItem, 0, len(list.Items))
	for _, b := range list.Items {
		var startStr, compStr string
		if b.Status.StartTime != nil {
			startStr = b.Status.StartTime.Time.Format(time.RFC3339)
		}
		if b.Status.CompletionTime != nil {
			compStr = b.Status.CompletionTime.Time.Format(time.RFC3339)
		}
		resp = append(resp, BuildListItem{
			Name:           b.Name,
			Phase:          b.Status.Phase,
			Message:        b.Status.Message,
			CreatedAt:      b.CreationTimestamp.Time.Format(time.RFC3339),
			StartTime:      startStr,
			CompletionTime: compStr,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func getBuild(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("k8s client error: %v", err), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("error fetching build: %v", err), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, BuildResponse{
		Name:             build.Name,
		Phase:            build.Status.Phase,
		Message:          build.Status.Message,
		ArtifactURL:      build.Status.ArtifactURL,
		ArtifactFileName: build.Status.ArtifactFileName,
		StartTime: func() string {
			if build.Status.StartTime != nil {
				return build.Status.StartTime.Time.Format(time.RFC3339)
			}
			return ""
		}(),
		CompletionTime: func() string {
			if build.Status.CompletionTime != nil {
				return build.Status.CompletionTime.Time.Format(time.RFC3339)
			}
			return ""
		}(),
	})
}

// getBuildTemplate returns a BuildRequest-like struct representing the inputs that produced a given build
func getBuildTemplate(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("k8s client error: %v", err), http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("error fetching build: %v", err), http.StatusInternalServerError)
		return
	}

	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: build.Spec.ManifestConfigMap, Namespace: namespace}, cm); err != nil {
		http.Error(w, fmt.Sprintf("error fetching manifest config: %v", err), http.StatusInternalServerError)
		return
	}

	// Rehydrate advanced args
	var aibExtra []string
	var aibOverride []string
	if v, ok := cm.Data["aib-extra-args.txt"]; ok {
		fields := strings.Fields(strings.TrimSpace(v))
		aibExtra = append(aibExtra, fields...)
	}
	if v, ok := cm.Data["aib-override-args.txt"]; ok {
		fields := strings.Fields(strings.TrimSpace(v))
		aibOverride = append(aibOverride, fields...)
	}

	manifestFileName := "manifest.aib.yml"
	var manifest string
	for k, v := range cm.Data {
		if k == "custom-definitions.env" || k == "aib-extra-args.txt" || k == "aib-override-args.txt" {
			continue
		}
		manifestFileName = k
		manifest = v
		break
	}

	var sourceFiles []string
	for _, line := range strings.Split(manifest, "\n") {
		s := strings.TrimSpace(line)
		if strings.HasPrefix(s, "source:") || strings.HasPrefix(s, "source_path:") {
			parts := strings.SplitN(s, ":", 2)
			if len(parts) == 2 {
				p := strings.TrimSpace(parts[1])
				p = strings.Trim(p, "'\"")
				if p != "" && !strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "http") {
					sourceFiles = append(sourceFiles, p)
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, BuildTemplateResponse{
		BuildRequest: BuildRequest{
			Name:                   build.Name,
			Manifest:               manifest,
			ManifestFileName:       manifestFileName,
			Distro:                 Distro(build.Spec.Distro),
			Target:                 Target(build.Spec.Target),
			Architecture:           Architecture(build.Spec.Architecture),
			ExportFormat:           ExportFormat(build.Spec.ExportFormat),
			Mode:                   Mode(build.Spec.Mode),
			AutomotiveImageBuilder: build.Spec.AutomotiveImageBuilder,
			CustomDefs:             nil,
			AIBExtraArgs:           aibExtra,
			AIBOverrideArgs:        aibOverride,
			ServeArtifact:          build.Spec.ServeArtifact,
		},
		SourceFiles: sourceFiles,
	})
}

func uploadFiles(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("k8s client error: %v", err), http.StatusInternalServerError)
		return
	}
	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(r.Context(), types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("error fetching build: %v", err), http.StatusInternalServerError)
		return
	}

	// Find upload pod
	podList := &corev1.PodList{}
	if err := k8sClient.List(r.Context(), podList,
		client.InNamespace(namespace),
		client.MatchingLabels{
			"automotive.sdv.cloud.redhat.com/imagebuild-name": name,
			"app.kubernetes.io/name":                          "upload-pod",
		},
	); err != nil {
		http.Error(w, fmt.Sprintf("error listing upload pods: %v", err), http.StatusInternalServerError)
		return
	}
	var uploadPod *corev1.Pod
	for i := range podList.Items {
		p := &podList.Items[i]
		if p.Status.Phase == corev1.PodRunning {
			uploadPod = p
			break
		}
	}
	if uploadPod == nil {
		http.Error(w, "upload pod not ready", http.StatusServiceUnavailable)
		return
	}

	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid multipart: %v", err), http.StatusBadRequest)
		return
	}

	restCfg, err := getRESTConfigFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("rest config: %v", err), http.StatusInternalServerError)
		return
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("read part: %v", err), http.StatusBadRequest)
			return
		}
		if part.FormName() != "file" {
			continue
		}
		dest := strings.TrimSpace(part.FileName())
		if dest == "" {
			http.Error(w, "missing destination filename", http.StatusBadRequest)
			return
		}

		cleanDest := path.Clean(dest)
		if strings.HasPrefix(cleanDest, "..") || strings.HasPrefix(cleanDest, "/") {
			http.Error(w, fmt.Sprintf("invalid destination path: %s", dest), http.StatusBadRequest)
			return
		}

		tmp, err := os.CreateTemp("", "upload-*")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tmpName := tmp.Name()
		defer tmp.Close()
		defer func() {
			_ = os.Remove(tmpName)
		}()

		if _, err := io.Copy(tmp, part); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := copyFileToPod(restCfg, namespace, uploadPod.Name, uploadPod.Spec.Containers[0].Name, tmpName, "/workspace/shared/"+cleanDest); err != nil {
			http.Error(w, fmt.Sprintf("stream to pod failed: %v", err), http.StatusInternalServerError)
			return
		}
	}

	original := build
	patched := original.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations["automotive.sdv.cloud.redhat.com/uploads-complete"] = "true"
	if err := k8sClient.Patch(r.Context(), patched, client.MergeFrom(original)); err != nil {
		http.Error(w, fmt.Sprintf("mark complete failed: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// streamArtifact streams the artifact file from the artifact pod to the client over HTTP.
func streamArtifact(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()
	ctx := r.Context()

	k8sClient, err := getClientFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("k8s client error: %v", err), http.StatusInternalServerError)
		return
	}

	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf("error fetching build: %v", err), http.StatusInternalServerError)
		return
	}

	if build.Status.Phase != "Completed" {
		http.Error(w, "artifact not available until build completes", http.StatusConflict)
		return
	}

	artifactFileName := build.Status.ArtifactFileName
	if artifactFileName == "" {
		var ext string
		switch build.Spec.ExportFormat {
		case "image":
			ext = ".raw"
		case "qcow2":
			ext = ".qcow2"
		default:
			ext = "." + build.Spec.ExportFormat
		}
		artifactFileName = fmt.Sprintf("%s-%s%s", build.Spec.Distro, build.Spec.Target, ext)
	}

	var artifactPod *corev1.Pod
	deadline := time.Now().Add(3 * time.Minute)
	for {
		podList := &corev1.PodList{}
		if err := k8sClient.List(ctx, podList,
			client.InNamespace(namespace),
			client.MatchingLabels{
				"app.kubernetes.io/name":                          "artifact-pod",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": name,
			}); err != nil {
			http.Error(w, fmt.Sprintf("error listing artifact pods: %v", err), http.StatusInternalServerError)
			return
		}
		for i := range podList.Items {
			p := &podList.Items[i]
			if p.Status.Phase == corev1.PodRunning {
				for _, cs := range p.Status.ContainerStatuses {
					if cs.Name == "fileserver" && cs.Ready {
						artifactPod = p
						break
					}
				}
			}
			if artifactPod != nil {
				break
			}
		}
		if artifactPod != nil {
			break
		}
		if time.Now().After(deadline) {
			http.Error(w, "artifact pod not ready", http.StatusServiceUnavailable)
			return
		}
		time.Sleep(3 * time.Second)
	}

	restCfg, err := getRESTConfigFromRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("rest config: %v", err), http.StatusInternalServerError)
		return
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		http.Error(w, fmt.Sprintf("clientset: %v", err), http.StatusInternalServerError)
		return
	}

	sourcePath := "/workspace/shared/" + artifactFileName

	// First, determine if the artifact is a directory on the fileserver container
	typeCheckReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   []string{"/bin/sh", "-c", "if [ -d '" + sourcePath + "' ]; then echo dir; else echo file; fi"},
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)
	typeExec, typeErr := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, typeCheckReq.URL())
	if typeErr != nil {
		http.Error(w, fmt.Sprintf("executor (type check): %v", typeErr), http.StatusInternalServerError)
		return
	}
	var typeStdout, typeStderr strings.Builder
	if err := typeExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &typeStdout, Stderr: &typeStderr}); err != nil {
		http.Error(w, fmt.Sprintf("type check stream: %v", err), http.StatusInternalServerError)
		return
	}
	isDir := strings.Contains(typeStdout.String(), "dir")
	if isDir {
		checkGzReq := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(artifactPod.Name).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: "fileserver",
				Command:   []string{"/bin/sh", "-c", "test -f '" + sourcePath + ".tar.gz' && echo yes || echo no"},
				Stdout:    true,
				Stderr:    true,
			}, kscheme.ParameterCodec)
		checkGzExec, gzErr := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, checkGzReq.URL())
		if gzErr == nil {
			var gzStdout, gzStderr strings.Builder
			_ = checkGzExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &gzStdout, Stderr: &gzStderr})
			if strings.Contains(gzStdout.String(), "yes") {
				isDir = false
				artifactFileName = artifactFileName + ".tar.gz"
				sourcePath = sourcePath + ".tar.gz"
			}
		}
	}

	if !isDir {
		gzFileName := artifactFileName
		if !strings.HasSuffix(strings.ToLower(gzFileName), ".gz") {
			gzFileName = gzFileName + ".gz"
		}

		gzReq := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(artifactPod.Name).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: "fileserver",
				Command:   []string{"sh", "-c", "gzip -c \"" + sourcePath + "\""},
				Stdout:    true,
				Stderr:    true,
			}, kscheme.ParameterCodec)
		gzExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, gzReq.URL())
		if err != nil {
			http.Error(w, fmt.Sprintf("executor (gzip): %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", gzFileName))
		w.Header().Set("X-AIB-Artifact-Type", "file")
		w.Header().Set("X-AIB-Compression", "gzip")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		_ = gzExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: w, Stderr: io.Discard})
		return
	}

	// Directory artifact: always stream gzip-compressed tar archive of the directory.
	tarFileName := artifactFileName + ".tar.gz"
	tarCmd := []string{"/bin/sh", "-c", "cd /workspace/shared && tar -czf - '" + artifactFileName + "'"}

	tarReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   tarCmd,
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)
	tarExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, tarReq.URL())
	if err != nil {
		http.Error(w, fmt.Sprintf("executor (tar): %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", tarFileName))
	w.Header().Set("X-AIB-Artifact-Type", "directory")
	w.Header().Set("X-AIB-Compression", "gzip")
	w.Header().Set("X-AIB-Archive-Root", artifactFileName)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	_ = tarExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: w, Stderr: io.Discard})
}

func copyFileToPod(config *rest.Config, namespace, podName, containerName, localPath, podPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}

	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		defer func() { tw.Close(); pw.Close() }()
		hdr := &tar.Header{Name: path.Base(podPath), Mode: 0600, Size: info.Size(), ModTime: info.ModTime()}
		if err := tw.WriteHeader(hdr); err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(tw, f); err != nil {
			pw.CloseWithError(err)
			return
		}
	}()

	destDir := path.Dir(podPath)
	cmd := []string{"/bin/sh", "-c", fmt.Sprintf("mkdir -p %s && tar -x -C %s", destDir, destDir)}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	req := clientset.CoreV1().RESTClient().Post().Resource("pods").Name(podName).Namespace(namespace).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, kscheme.ParameterCodec)
	executor, err := remotecommand.NewSPDYExecutor(config, http.MethodPost, req.URL())
	if err != nil {
		return err
	}
	return executor.StreamWithContext(context.Background(), remotecommand.StreamOptions{Stdin: pr, Stdout: io.Discard, Stderr: io.Discard})
}

func setOwnerRef(ctx context.Context, c client.Client, namespace, configMapName string, owner *automotivev1.ImageBuild) error {
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, cm); err != nil {
		return err
	}
	cm.OwnerReferences = []metav1.OwnerReference{
		*metav1.NewControllerRef(owner, automotivev1.GroupVersion.WithKind("ImageBuild")),
	}
	return c.Update(ctx, cm)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func resolveNamespace() string {
	if ns := strings.TrimSpace(os.Getenv("BUILD_API_NAMESPACE")); ns != "" {
		return ns
	}
	if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		ns := strings.TrimSpace(string(data))
		if ns != "" {
			return ns
		}
	}
	return "default"
}

func getRESTConfigFromRequest(_ *http.Request) (*rest.Config, error) {
	var cfg *rest.Config
	var err error
	cfg, err = rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("failed to build kube config: %w", err)
		}
	}
	cfgCopy := rest.CopyConfig(cfg)
	cfgCopy.Timeout = 10 * time.Minute
	return cfgCopy, nil
}

func getClientFromRequest(r *http.Request) (client.Client, error) {
	cfg, err := getRESTConfigFromRequest(r)
	if err != nil {
		return nil, err
	}

	scheme := runtime.NewScheme()
	if err := automotivev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add automotive scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add core scheme: %w", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}
	return c, nil
}

func isAuthenticated(r *http.Request) bool {
	// Extract bearer token presented by the client (direct or via OAuth proxy)
	authHeader := r.Header.Get("Authorization")
	token := ""
	if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}
	if token == "" {
		token = r.Header.Get("X-Forwarded-Access-Token")
	}
	if strings.TrimSpace(token) == "" {
		return false
	}

	// Validate token via TokenReview using server credentials
	cfg, err := getRESTConfigFromRequest(r)
	if err != nil {
		return false
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return false
	}
	tr := &authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}
	res, err := clientset.AuthenticationV1().TokenReviews().Create(r.Context(), tr, metav1.CreateOptions{})
	if err != nil {
		return false
	}
	return res.Status.Authenticated
}
