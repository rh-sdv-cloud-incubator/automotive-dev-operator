package buildapi

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

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
)

type APIServer struct {
	server *http.Server
	addr   string
}

// NewAPIServer creates a new API server runnable
func NewAPIServer(addr string) *APIServer {
	return &APIServer{
		addr:   addr,
		server: &http.Server{Addr: addr, Handler: createHandler()},
	}
}

// Start implements manager.Runnable
func (a *APIServer) Start(ctx context.Context) error {

	go func() {
		log.Printf("build-api listening on %s", a.addr)
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("build-api server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down build-api server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		log.Printf("build-api server forced to shutdown: %v", err)
		return err
	}
	log.Println("build-api server exited")
	return nil
}

func createHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/builds", handleBuilds)
	mux.HandleFunc("/v1/builds/", handleBuildByName)
	return mux
}

// StartServer starts the REST API server on the given address in a goroutine and returns the server
func StartServer(addr string) (*http.Server, error) {
	server := &http.Server{Addr: addr, Handler: createHandler()}
	apiServer := &APIServer{addr: addr, server: server}
	go func() {
		if err := apiServer.Start(context.Background()); err != nil {
			log.Printf("StartServer error: %v", err)
		}
	}()
	return server, nil
}

func handleBuilds(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		createBuild(w, r)
	case http.MethodGet:
		listBuilds(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleBuildByName(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	name := parts[2]

	switch r.Method {
	case http.MethodGet:
		getBuild(w, r, name)
	case http.MethodPost:
		if len(parts) == 4 && parts[3] == "uploads" {
			uploadFiles(w, r, name)
			return
		}
		http.NotFound(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	k8sClient, err := getClientFromEnv()
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
	if len(req.AIBExtraArgs) > 0 {
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
			StorageClass:           req.StorageClass,
			RuntimeClassName:       req.RuntimeClassName,
			ServeArtifact:          false,
			ServeExpiryHours:       24,
			ManifestConfigMap:      cfgName,
			InputFilesServer:       strings.Contains(req.Manifest, "source_path"),
			ExposeRoute:            false,
		},
	}
	if err := k8sClient.Create(ctx, imageBuild); err != nil {
		http.Error(w, fmt.Sprintf("error creating ImageBuild: %v", err), http.StatusInternalServerError)
		return
	}

	if err := setOwnerRef(ctx, k8sClient, namespace, cfgName, imageBuild); err != nil {
		log.Printf("warning: failed to set owner on ConfigMap: %v", err)
	}

	writeJSON(w, http.StatusAccepted, BuildResponse{
		Name:    req.Name,
		Phase:   "Building",
		Message: "Build triggered",
	})
}

func listBuilds(w http.ResponseWriter, r *http.Request) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromEnv()
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
		resp = append(resp, BuildListItem{
			Name:      b.Name,
			Phase:     b.Status.Phase,
			Message:   b.Status.Message,
			CreatedAt: b.CreationTimestamp.Time.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func getBuild(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getClientFromEnv()
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
	})
}

func uploadFiles(w http.ResponseWriter, r *http.Request, name string) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromEnv()
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

	restCfg, err := getRESTConfig()
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

func getRESTConfig() (*rest.Config, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		kubeconfig := os.Getenv("KUBECONFIG")
		cfg, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}
	cfgCopy := rest.CopyConfig(cfg)
	cfgCopy.Timeout = 10 * time.Minute
	return cfgCopy, nil
}

func getClientFromEnv() (client.Client, error) {
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
