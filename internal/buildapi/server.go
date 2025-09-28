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

	"github.com/gin-gonic/gin"
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
	router *gin.Engine
	addr   string
	log    logr.Logger
}

//go:embed openapi.yaml
var embeddedOpenAPI []byte

type ctxKeyReqID struct{}

// NewAPIServer creates a new API server
func NewAPIServer(addr string, logger logr.Logger) *APIServer {
	// Gin mode should be controlled by environment, not by which constructor is used
	if os.Getenv("GIN_MODE") == "" {
		// Default to release mode for production safety
		gin.SetMode(gin.ReleaseMode)
	}

	a := &APIServer{addr: addr, log: logger}
	a.router = a.createRouter()
	a.server = &http.Server{Addr: addr, Handler: a.router}
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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := a.server.Shutdown(shutdownCtx); err != nil {
		a.log.Error(err, "build-api server forced to shutdown")
		return err
	}
	a.log.Info("build-api server exited")
	return nil
}

func (a *APIServer) createRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	// Add request ID and logging middleware
	router.Use(func(c *gin.Context) {
		reqID := uuid.New().String()
		c.Set("reqID", reqID)
		a.log.Info("http request", "method", c.Request.Method, "path", c.Request.URL.Path, "reqID", reqID)
		fmt.Printf("HTTP Request: %s %s\n", c.Request.Method, c.Request.URL.Path)
		c.Next()
	})

	v1 := router.Group("/v1")
	{
		v1.GET("/healthz", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
		})

		v1.GET("/openapi.yaml", func(c *gin.Context) {
			c.Data(http.StatusOK, "application/yaml", embeddedOpenAPI)
		})

		// Builds endpoints with authentication middleware
		buildsGroup := v1.Group("/builds")
		buildsGroup.Use(a.authMiddleware())
		{
			buildsGroup.POST("", a.handleCreateBuild)
			buildsGroup.GET("", a.handleListBuilds)
			buildsGroup.GET("/sse", a.handleStreamBuildsSSE)
			buildsGroup.GET("/:name", a.handleGetBuild)
			buildsGroup.GET("/:name/logs", a.handleStreamLogs)
			buildsGroup.GET("/:name/logs/sse", a.handleStreamLogsSSE)
			buildsGroup.GET("/:name/artifacts", a.handleListArtifacts)
			buildsGroup.GET("/:name/artifacts/:file", a.handleStreamArtifactPart)
			buildsGroup.GET("/:name/artifact/:filename", a.handleStreamArtifactByFilename)
			buildsGroup.GET("/:name/template", a.handleGetBuildTemplate)
			buildsGroup.POST("/:name/uploads", a.handleUploadFiles)
		}
	}

	return router
}

// StartServer starts the REST API server on the given address in a goroutine and returns the server
func StartServer(addr string, logger logr.Logger) (*http.Server, error) {
	api := NewAPIServer(addr, logger)
	server := api.server
	go func() {
		if err := api.Start(context.Background()); err != nil {
			logger.Error(err, "failed to start build-api server")
		}
	}()
	return server, nil
}

// authMiddleware provides authentication middleware for Gin
func (a *APIServer) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isAuthenticated(c) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (a *APIServer) handleCreateBuild(c *gin.Context) {
	a.log.Info("create build", "reqID", c.GetString("reqID"))
	createBuild(c)
}

func (a *APIServer) handleListBuilds(c *gin.Context) {
	a.log.Info("list builds", "reqID", c.GetString("reqID"))
	listBuilds(c)
}

func (a *APIServer) handleGetBuild(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("get build", "build", name, "reqID", c.GetString("reqID"))
	getBuild(c, name)
}

func (a *APIServer) handleStreamLogs(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("logs requested", "build", name, "reqID", c.GetString("reqID"))
	streamLogs(c, name)
}

func (a *APIServer) handleStreamLogsSSE(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("logs SSE requested", "build", name, "reqID", c.GetString("reqID"))

	streamLogsSSE(c, name)
}

func (a *APIServer) handleStreamBuildsSSE(c *gin.Context) {
	a.log.Info("builds SSE requested", "reqID", c.GetString("reqID"))
	streamBuildsSSE(c)
}

func (a *APIServer) handleListArtifacts(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("artifacts list requested", "build", name, "reqID", c.GetString("reqID"))
	a.listArtifacts(c, name)
}

func (a *APIServer) handleStreamArtifactPart(c *gin.Context) {
	name := c.Param("name")
	file := c.Param("file")
	a.log.Info("artifact item requested", "build", name, "file", file, "reqID", c.GetString("reqID"))
	a.streamArtifactPart(c, name, file)
}

func (a *APIServer) handleStreamArtifactByFilename(c *gin.Context) {
	name := c.Param("name")
	filename := c.Param("filename")
	a.log.Info("artifact by filename requested", "build", name, "filename", filename, "reqID", c.GetString("reqID"))
	a.streamArtifactByFilename(c, name, filename)
}

func (a *APIServer) handleGetBuildTemplate(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("template requested", "build", name, "reqID", c.GetString("reqID"))
	getBuildTemplate(c, name)
}

func (a *APIServer) handleUploadFiles(c *gin.Context) {
	name := c.Param("name")
	a.log.Info("uploads", "build", name, "reqID", c.GetString("reqID"))
	uploadFiles(c, name)
}

func streamLogs(c *gin.Context, name string) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	ctx := c.Request.Context()
	var podName string

	ib := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ib); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	tr := strings.TrimSpace(ib.Status.TaskRunName)
	if tr == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "logs not available yet"})
		return
	}
	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	quickCS, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pods, err := quickCS.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "tekton.dev/taskRun=" + tr})
	if err != nil || len(pods.Items) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "logs not available yet"})
		return
	}
	podName = pods.Items[0].Name

	cfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Set up streaming response
	c.Writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write([]byte("Waiting for logs...\n"))
	c.Writer.Flush()

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
			fmt.Fprintf(c.Writer, "\n[Error: %v]\n", err)
			c.Writer.Flush()
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
				c.Writer.Flush()
			}
			hadStream = true

			_, _ = c.Writer.Write([]byte("\n===== Logs from " + strings.TrimPrefix(cName, "step-") + " =====\n\n"))
			c.Writer.Flush()
			// Stream with proper error handling and context cancellation
			func() {
				defer stream.Close()

				// Create a buffer for chunked reading
				buf := make([]byte, 4096)
				for {
					select {
					case <-ctx.Done():
						return
					default:
					}

					n, err := stream.Read(buf)
					if n > 0 {
						if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
							return
						}
						c.Writer.Flush()
					}

					if err != nil {
						if err != io.EOF {
							var errMsg []byte
							errMsg = fmt.Appendf(errMsg, "\n[Stream error: %v]\n", err)
							_, _ = c.Writer.Write(errMsg)
							c.Writer.Flush()
						}
						return
					}
				}
			}()

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
			_, _ = c.Writer.Write([]byte("."))
			if f, ok := c.Writer.(http.Flusher); ok {
				f.Flush()
			}
		}
	}

	if !hadStream {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "logs unavailable: " + strings.Join(lastErrs, "; ")})
		return
	}

	_, _ = c.Writer.Write([]byte("\n[Log streaming completed]\n"))
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}
}

func streamLogsSSE(c *gin.Context, name string) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "Cache-Control")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		sendSSEEvent(c, "message", "", fmt.Sprintf("ERROR: Client error: %v", err))
		c.Writer.Flush()
		return
	}

	ctx := c.Request.Context()
	var podName string

	ib := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, ib); err != nil {
		if k8serrors.IsNotFound(err) {
			sendSSEEvent(c, "message", "", "ERROR: Build not found")
		} else {
			sendSSEEvent(c, "message", "", fmt.Sprintf("ERROR: Build lookup error: %v", err))
		}
		c.Writer.Flush()
		return
	}
	tr := strings.TrimSpace(ib.Status.TaskRunName)
	if tr == "" {
		sendSSEEvent(c, "waiting", "", "Build not started yet, waiting for logs...")
		c.Writer.Flush()
		return
	}
	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		sendSSEEvent(c, "message", "", fmt.Sprintf("ERROR: Config error: %v", err))
		c.Writer.Flush()
		return
	}
	quickCS, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		sendSSEEvent(c, "message", "", fmt.Sprintf("ERROR: Kubernetes client error: %v", err))
		c.Writer.Flush()
		return
	}
	pods, err := quickCS.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: "tekton.dev/taskRun=" + tr})
	if err != nil || len(pods.Items) == 0 {
		sendSSEEvent(c, "waiting", "", "Build pods not ready yet, waiting for logs...")
		c.Writer.Flush()
		return
	}
	podName = pods.Items[0].Name

	cfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		sendSSEEvent(c, "message", "", fmt.Sprintf("Config error: %v", err))
		c.Writer.Flush()
		return
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		sendSSEEvent(c, "message", "", fmt.Sprintf("Kubernetes client error: %v", err))
		c.Writer.Flush()
		return
	}

	sendSSEEvent(c, "connected", "", "Log stream connected")
	c.Writer.Flush()

	var hadStream bool
	streamed := make(map[string]bool)
	var lastErrs []string

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sendSSEEvent(c, "ping", "", "")
				c.Writer.Flush()
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			sendSSEEvent(c, "disconnected", "", "Connection closed")
			c.Writer.Flush()
			return
		default:
		}

		pod, err := cs.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if err != nil {
			sendSSEEvent(c, "error", "", fmt.Sprintf("Error: %v", err))
			c.Writer.Flush()
			return
		}

		stepNames := make([]string, 0, len(pod.Spec.Containers))
		for _, container := range pod.Spec.Containers {
			if strings.HasPrefix(container.Name, "step-") {
				stepNames = append(stepNames, container.Name)
			}
		}
		if len(stepNames) == 0 {
			for _, container := range pod.Spec.Containers {
				stepNames = append(stepNames, container.Name)
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
				c.Writer.Flush()
			}
			hadStream = true

			stepName := strings.TrimPrefix(cName, "step-")
			sendSSEEvent(c, "step", stepName, "===== Logs from "+stepName+" =====")
			c.Writer.Flush()

			func() {
				defer stream.Close()

				buf := make([]byte, 4096)
				var lineBuffer strings.Builder

				for {
					select {
					case <-ctx.Done():
						return
					default:
					}

					n, err := stream.Read(buf)
					if n > 0 {
						chunk := string(buf[:n])
						lineBuffer.WriteString(chunk)

						lines := strings.Split(lineBuffer.String(), "\n")
						lineBuffer.Reset()

						if len(lines) > 1 {
							lineBuffer.WriteString(lines[len(lines)-1])
							lines = lines[:len(lines)-1]
						}

						for _, line := range lines {
							if strings.TrimSpace(line) != "" {
								sendSSEEvent(c, "log", stepName, line)
								c.Writer.Flush()
							}
						}
					}

					if err != nil {
						if err != io.EOF {
							sendSSEEvent(c, "error", stepName, fmt.Sprintf("Stream error: %v", err))
							c.Writer.Flush()
						}
						return
					}
				}
			}()

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
			sendSSEEvent(c, "waiting", "", "Waiting for logs...")
			c.Writer.Flush()
		}
	}

	if !hadStream {
		sendSSEEvent(c, "error", "", "logs unavailable: "+strings.Join(lastErrs, "; "))
		c.Writer.Flush()
		return
	}

	sendSSEEvent(c, "completed", "", "Log streaming completed")
	c.Writer.Flush()
}

func streamBuildsSSE(c *gin.Context) {
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
	c.Writer.Header().Set("Access-Control-Allow-Headers", "Cache-Control")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		sendSSEEvent(c, "error", "", fmt.Sprintf("Client error: %v", err))
		c.Writer.Flush()
		return
	}

	ctx := c.Request.Context()

	// Send initial connection event
	sendSSEEvent(c, "connected", "", "Builds stream connected")
	c.Writer.Flush()

	// Send initial list of builds
	var buildList automotivev1.ImageBuildList
	if err := k8sClient.List(ctx, &buildList, client.InNamespace(namespace)); err != nil {
		sendSSEEvent(c, "error", "", fmt.Sprintf("Failed to list builds: %v", err))
		c.Writer.Flush()
		return
	}

	// Convert and send initial builds
	builds := convertImageBuildList(&buildList)
	buildsData, err := json.Marshal(builds)
	if err != nil {
		sendSSEEvent(c, "error", "", fmt.Sprintf("Failed to marshal builds: %v", err))
		c.Writer.Flush()
		return
	}

	sendSSEEvent(c, "initial-list", "", string(buildsData))
	c.Writer.Flush()

	// Store the last known state of builds for comparison
	lastBuilds := make(map[string]BuildListItem)
	for _, build := range builds {
		lastBuilds[build.Name] = build
	}

	// Send keepalive pings and poll for changes
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			sendSSEEvent(c, "disconnected", "", "Connection closed")
			c.Writer.Flush()
			return

		case <-pingTicker.C:
			sendSSEEvent(c, "ping", "", "")
			c.Writer.Flush()

		case <-ticker.C:
			// Poll for build changes
			var currentBuildList automotivev1.ImageBuildList
			if err := k8sClient.List(ctx, &currentBuildList, client.InNamespace(namespace)); err != nil {
				sendSSEEvent(c, "error", "", fmt.Sprintf("Failed to list builds: %v", err))
				c.Writer.Flush()
				continue
			}

			currentBuilds := convertImageBuildList(&currentBuildList)
			currentBuildsMap := make(map[string]BuildListItem)
			for _, build := range currentBuilds {
				currentBuildsMap[build.Name] = build
			}

			// Check for new or updated builds
			for name, currentBuild := range currentBuildsMap {
				if lastBuild, exists := lastBuilds[name]; !exists {
					// New build
					data, err := json.Marshal(currentBuild)
					if err == nil {
						sendSSEEvent(c, "build-created", name, string(data))
						c.Writer.Flush()
					}
				} else if lastBuild != currentBuild {
					// Updated build
					data, err := json.Marshal(currentBuild)
					if err == nil {
						sendSSEEvent(c, "build-updated", name, string(data))
						c.Writer.Flush()
					}
				}
			}

			// Check for deleted builds
			for name := range lastBuilds {
				if _, exists := currentBuildsMap[name]; !exists {
					// Deleted build
					data, err := json.Marshal(lastBuilds[name])
					if err == nil {
						sendSSEEvent(c, "build-deleted", name, string(data))
						c.Writer.Flush()
					}
				}
			}

			// Update last known state
			lastBuilds = currentBuildsMap
		}
	}
}

// convertImageBuildList converts a Kubernetes ImageBuildList to the API response format
func convertImageBuildList(list *automotivev1.ImageBuildList) []BuildListItem {
	resp := make([]BuildListItem, 0, len(list.Items))
	for _, b := range list.Items {
		resp = append(resp, convertImageBuildToListItem(&b))
	}
	return resp
}

// convertImageBuild converts a Kubernetes ImageBuild to the API response format
func convertImageBuild(b *automotivev1.ImageBuild) BuildListItem {
	return convertImageBuildToListItem(b)
}

// convertImageBuildToListItem converts a single ImageBuild to BuildListItem
func convertImageBuildToListItem(b *automotivev1.ImageBuild) BuildListItem {
	var startStr, compStr string
	if b.Status.StartTime != nil {
		startStr = b.Status.StartTime.Time.Format(time.RFC3339)
	}
	if b.Status.CompletionTime != nil {
		compStr = b.Status.CompletionTime.Time.Format(time.RFC3339)
	}
	return BuildListItem{
		Name:           b.Name,
		Phase:          b.Status.Phase,
		Message:        b.Status.Message,
		RequestedBy:    b.Annotations["automotive.sdv.cloud.redhat.com/requested-by"],
		CreatedAt:      b.CreationTimestamp.Time.Format(time.RFC3339),
		StartTime:      startStr,
		CompletionTime: compStr,
	}
}

// sendSSEEvent sends a Server-Sent Event
func sendSSEEvent(c *gin.Context, event, step, data string) {
	if event != "" {
		c.Writer.WriteString("event: " + event + "\n")
	}
	if step != "" {
		c.Writer.WriteString("id: " + step + "\n")
	}
	if data != "" {
		// Escape newlines in data
		escapedData := strings.ReplaceAll(data, "\n", "\\n")
		c.Writer.WriteString("data: " + escapedData + "\n")
	}
	c.Writer.WriteString("\n")
}

func createRegistrySecret(ctx context.Context, k8sClient client.Client, namespace, buildName string, creds *RegistryCredentials) (string, error) {
	if creds == nil || !creds.Enabled {
		return "", nil
	}

	secretName := fmt.Sprintf("%s-registry-auth", buildName)
	secretData := make(map[string][]byte)

	switch creds.AuthType {
	case "username-password":
		if creds.RegistryURL == "" || creds.Username == "" || creds.Password == "" {
			return "", fmt.Errorf("registry URL, username, and password are required for username-password authentication")
		}
		secretData["REGISTRY_URL"] = []byte(creds.RegistryURL)
		secretData["REGISTRY_USERNAME"] = []byte(creds.Username)
		secretData["REGISTRY_PASSWORD"] = []byte(creds.Password)
	case "token":
		if creds.RegistryURL == "" || creds.Token == "" {
			return "", fmt.Errorf("registry URL and token are required for token authentication")
		}
		secretData["REGISTRY_URL"] = []byte(creds.RegistryURL)
		secretData["REGISTRY_TOKEN"] = []byte(creds.Token)
	case "docker-config":
		if creds.DockerConfig == "" {
			return "", fmt.Errorf("docker config is required for docker-config authentication")
		}
		secretData["REGISTRY_AUTH_FILE_CONTENT"] = []byte(creds.DockerConfig)
	default:
		return "", fmt.Errorf("unsupported authentication type: %s", creds.AuthType)
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                  "build-api",
				"app.kubernetes.io/part-of":                     "automotive-dev",
				"app.kubernetes.io/created-by":                  "automotive-dev-build-api",
				"automotive.sdv.cloud.redhat.com/resource-type": "registry-auth",
				"automotive.sdv.cloud.redhat.com/build-name":    buildName,
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: secretData,
	}

	if err := k8sClient.Create(ctx, secret); err != nil {
		return "", fmt.Errorf("failed to create registry secret: %w", err)
	}

	return secretName, nil
}

func createBuild(c *gin.Context) {
	var req BuildRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	needsUpload := strings.Contains(req.Manifest, "source_path")

	if req.Name == "" || req.Manifest == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and manifest are required"})
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

	if strings.TrimSpace(req.Compression) == "" {
		req.Compression = "gzip"
	}
	if req.Compression != "lz4" && req.Compression != "gzip" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid compression: must be lz4 or gzip"})
		return
	}

	if !req.Distro.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "distro cannot be empty"})
		return
	}
	if !req.Target.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "target cannot be empty"})
		return
	}
	if !req.Architecture.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "architecture cannot be empty"})
		return
	}
	if !req.ExportFormat.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "exportFormat cannot be empty"})
		return
	}
	if !req.Mode.IsValid() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mode cannot be empty"})
		return
	}
	if req.AutomotiveImageBuilder == "" {
		req.AutomotiveImageBuilder = "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0"
	}
	if req.ManifestFileName == "" {
		req.ManifestFileName = "manifest.aib.yml"
	}

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	ctx := c.Request.Context()
	namespace := resolveNamespace()

	requestedBy := resolveRequester(c)

	existing := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: req.Name, Namespace: namespace}, existing); err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("ImageBuild %s already exists", req.Name)})
		return
	} else if !k8serrors.IsNotFound(err) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error checking existing build: %v", err)})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating manifest ConfigMap: %v", err)})
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

	var envSecretRef string
	if req.RegistryCredentials != nil && req.RegistryCredentials.Enabled {
		secretName, err := createRegistrySecret(ctx, k8sClient, namespace, req.Name, req.RegistryCredentials)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating registry secret: %v", err)})
			return
		}
		envSecretRef = secretName
	}

	imageBuild := &automotivev1.ImageBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"automotive.sdv.cloud.redhat.com/requested-by": requestedBy,
			},
		},
		Spec: automotivev1.ImageBuildSpec{
			Distro:                 string(req.Distro),
			Target:                 string(req.Target),
			Architecture:           string(req.Architecture),
			ExportFormat:           string(req.ExportFormat),
			Mode:                   string(req.Mode),
			AutomotiveImageBuilder: req.AutomotiveImageBuilder,
			StorageClass:           req.StorageClass,
			ServeArtifact:          req.ServeArtifact,
			ExposeRoute:            req.ServeArtifact,
			ServeExpiryHours:       serveExpiryHours,
			ManifestConfigMap:      cfgName,
			InputFilesServer:       needsUpload,
			EnvSecretRef:           envSecretRef,
			Compression:            req.Compression,
		},
	}
	if err := k8sClient.Create(ctx, imageBuild); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error creating ImageBuild: %v", err)})
		return
	}

	if err := setOwnerRef(ctx, k8sClient, namespace, cfgName, imageBuild); err != nil {
		// best-effort
	}

	if envSecretRef != "" {
		if err := setOwnerRef(ctx, k8sClient, namespace, envSecretRef, imageBuild); err != nil {
			// best-effort
		}
	}

	writeJSON(c, http.StatusAccepted, BuildResponse{
		Name:        req.Name,
		Phase:       "Building",
		Message:     "Build triggered",
		RequestedBy: requestedBy,
	})
}

func listBuilds(c *gin.Context) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	ctx := c.Request.Context()
	list := &automotivev1.ImageBuildList{}
	if err := k8sClient.List(ctx, list, client.InNamespace(namespace)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error listing builds: %v", err)})
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
			RequestedBy:    b.Annotations["automotive.sdv.cloud.redhat.com/requested-by"],
			CreatedAt:      b.CreationTimestamp.Time.Format(time.RFC3339),
			StartTime:      startStr,
			CompletionTime: compStr,
		})
	}
	writeJSON(c, http.StatusOK, resp)
}

func getBuild(c *gin.Context, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	ctx := c.Request.Context()
	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching build: %v", err)})
		return
	}

	writeJSON(c, http.StatusOK, BuildResponse{
		Name:             build.Name,
		Phase:            build.Status.Phase,
		Message:          build.Status.Message,
		RequestedBy:      build.Annotations["automotive.sdv.cloud.redhat.com/requested-by"],
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
func getBuildTemplate(c *gin.Context, name string) {
	namespace := resolveNamespace()
	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	ctx := c.Request.Context()
	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching build: %v", err)})
		return
	}

	cm := &corev1.ConfigMap{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: build.Spec.ManifestConfigMap, Namespace: namespace}, cm); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching manifest config: %v", err)})
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

	writeJSON(c, http.StatusOK, BuildTemplateResponse{
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
			Compression:            build.Spec.Compression,
		},
		SourceFiles: sourceFiles,
	})
}

func uploadFiles(c *gin.Context, name string) {
	namespace := resolveNamespace()

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}
	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(c.Request.Context(), types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching build: %v", err)})
		return
	}

	// Find upload pod
	podList := &corev1.PodList{}
	if err := k8sClient.List(c.Request.Context(), podList,
		client.InNamespace(namespace),
		client.MatchingLabels{
			"automotive.sdv.cloud.redhat.com/imagebuild-name": name,
			"app.kubernetes.io/name":                          "upload-pod",
		},
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error listing upload pods: %v", err)})
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
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "upload pod not ready"})
		return
	}

	reader, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid multipart: %v", err)})
		return
	}

	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("rest config: %v", err)})
		return
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("read part: %v", err)})
			return
		}
		if part.FormName() != "file" {
			continue
		}
		dest := strings.TrimSpace(part.FileName())
		if dest == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing destination filename"})
			return
		}

		cleanDest := path.Clean(dest)
		if strings.HasPrefix(cleanDest, "..") || strings.HasPrefix(cleanDest, "/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid destination path: %s", dest)})
			return
		}

		tmp, err := os.CreateTemp("", "upload-*")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		tmpName := tmp.Name()
		defer tmp.Close()
		defer func() {
			_ = os.Remove(tmpName)
		}()

		if _, err := io.Copy(tmp, part); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if err := copyFileToPod(restCfg, namespace, uploadPod.Name, uploadPod.Spec.Containers[0].Name, tmpName, "/workspace/shared/"+cleanDest); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("stream to pod failed: %v", err)})
			return
		}
	}

	original := build
	patched := original.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = map[string]string{}
	}
	patched.Annotations["automotive.sdv.cloud.redhat.com/uploads-complete"] = "true"
	if err := k8sClient.Patch(c.Request.Context(), patched, client.MergeFrom(original)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("mark complete failed: %v", err)})
		return
	}
	writeJSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *APIServer) listArtifacts(c *gin.Context, name string) {
	namespace := resolveNamespace()
	ctx := c.Request.Context()

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching build: %v", err)})
		return
	}

	if build.Status.Phase != "Completed" {
		c.JSON(http.StatusConflict, gin.H{"error": "artifact not available until build completes"})
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
	deadline := time.Now().Add(2 * time.Minute)
	for {
		podList := &corev1.PodList{}
		if err := k8sClient.List(ctx, podList,
			client.InNamespace(namespace),
			client.MatchingLabels{
				"app.kubernetes.io/name":                          "artifact-pod",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": name,
			}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error listing artifact pods: %v", err)})
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
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "artifact pod not ready"})
			return
		}
		time.Sleep(2 * time.Second)
	}

	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("rest config: %v", err)})
		return
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("clientset: %v", err)})
		return
	}

	partsDir := "/workspace/shared/" + artifactFileName + "-parts"
	listReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   []string{"sh", "-c", "set -e; dir=\"" + partsDir + "\"; if [ ! -d \"$dir\" ]; then echo MISSING; exit 0; fi; for f in \"$dir\"/*; do [ -f \"$f\" ] || continue; n=$(basename \"$f\"); s=$(wc -c < \"$f\"); printf '%s:%s\\n' \"$n\" \"$s\"; done"},
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)
	listExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, listReq.URL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("executor (list): %v", err)})
		return
	}
	var out strings.Builder
	if err := listExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &out, Stderr: io.Discard}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("list stream: %v", err)})
		return
	}
	trim := strings.TrimSpace(out.String())
	if trim == "" || trim == "MISSING" {
		// No parts available
		writeJSON(c, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	lines := strings.Split(trim, "\n")
	type item struct {
		Name      string `json:"name"`
		SizeBytes string `json:"sizeBytes"`
	}
	items := make([]item, 0, len(lines))
	for _, ln := range lines {
		p := strings.SplitN(strings.TrimSpace(ln), ":", 2)
		if len(p) != 2 {
			continue
		}
		items = append(items, item{Name: p[0], SizeBytes: strings.TrimSpace(p[1])})
	}
	writeJSON(c, http.StatusOK, map[string]any{"items": items})
}

func (a *APIServer) streamArtifactPart(c *gin.Context, name, file string) {
	namespace := resolveNamespace()
	ctx := c.Request.Context()

	if strings.Contains(file, "/") || strings.Contains(file, "..") || strings.TrimSpace(file) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file name"})
		return
	}

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching build: %v", err)})
		return
	}

	if build.Status.Phase != "Completed" {
		c.JSON(http.StatusConflict, gin.H{"error": "artifact not available until build completes"})
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
	deadline := time.Now().Add(2 * time.Minute)
	for {
		podList := &corev1.PodList{}
		if err := k8sClient.List(ctx, podList,
			client.InNamespace(namespace),
			client.MatchingLabels{
				"app.kubernetes.io/name":                          "artifact-pod",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": name,
			}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error listing artifact pods: %v", err)})
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
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "artifact pod not ready"})
			return
		}
		time.Sleep(2 * time.Second)
	}

	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("rest config: %v", err)})
		return
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("clientset: %v", err)})
		return
	}

	gzPath := "/workspace/shared/" + artifactFileName + "-parts/" + file
	// Check existence and size
	sizeReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   []string{"sh", "-c", "if [ -f \"" + gzPath + "\" ]; then wc -c < \"" + gzPath + "\"; else echo MISSING; fi"},
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)
	sizeExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, sizeReq.URL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("executor (size): %v", err)})
		return
	}
	var sizeStdout strings.Builder
	if err := sizeExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &sizeStdout, Stderr: io.Discard}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("size stream: %v", err)})
		return
	}
	sz := strings.TrimSpace(sizeStdout.String())
	if sz == "" || sz == "MISSING" {
		c.JSON(http.StatusNotFound, gin.H{"error": "artifact item not found"})
		return
	}

	streamReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   []string{"cat", gzPath},
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)
	streamExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, streamReq.URL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("executor (stream): %v", err)})
		return
	}

	c.Writer.Header().Set("Content-Type", "application/gzip")
	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file))
	c.Writer.Header().Set("Content-Length", sz)
	c.Writer.Header().Set("X-AIB-Artifact-Type", "file")
	c.Writer.Header().Set("X-AIB-Compression", "gzip")
	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}

	_ = streamExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: c.Writer, Stderr: io.Discard})
}

// streamArtifactByFilename streams the specified artifact file from the artifact pod to the client over HTTP
func (a *APIServer) streamArtifactByFilename(c *gin.Context, name, filename string) {
	namespace := resolveNamespace()
	ctx := c.Request.Context()

	if strings.Contains(filename, "/") || strings.Contains(filename, "..") || strings.TrimSpace(filename) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file name"})
		return
	}

	k8sClient, err := getClientFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("k8s client error: %v", err)})
		return
	}

	build := &automotivev1.ImageBuild{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
		if k8serrors.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error fetching build: %v", err)})
		return
	}

	if build.Status.Phase != "Completed" {
		c.JSON(http.StatusConflict, gin.H{"error": "artifact not available until build completes"})
		return
	}

	// Only allow the exact final artifact file name or files from the -parts directory
	expected := strings.TrimSpace(build.Status.ArtifactFileName)
	base := path.Base(filename)
	allowed := base == expected

	if !allowed {
		// Check if it's a part file (from -parts directory)
		if strings.HasSuffix(base, ".gz") || strings.HasSuffix(base, ".lz4") {
			// Allow parts that follow the pattern: <expected>-parts/<filename>
			if strings.Contains(base, ".tar.") || strings.HasPrefix(base, strings.TrimSuffix(expected, path.Ext(expected))) {
				allowed = true
			}
		}
	}

	if !allowed {
		c.JSON(http.StatusForbidden, gin.H{"error": "file not allowed"})
		return
	}

	// Get REST config and clientset for pod operations
	restCfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("rest config: %v", err)})
		return
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("clientset: %v", err)})
		return
	}

	// Find the artifact pod
	var artifactPod *corev1.Pod
	deadline := time.Now().Add(2 * time.Minute)
	for {
		podList := &corev1.PodList{}
		if err := k8sClient.List(ctx, podList,
			client.InNamespace(namespace),
			client.MatchingLabels{
				"app.kubernetes.io/name":                          "artifact-pod",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": name,
			}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("error listing artifact pods: %v", err)})
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
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "artifact pod not ready"})
			return
		}
		time.Sleep(2 * time.Second)
	}

	podPath := "/workspace/shared/" + base

	// Check if file exists and get size
	sizeReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   []string{"sh", "-c", "if [ -f '" + podPath + "' ]; then wc -c < '" + podPath + "'; else echo MISSING; fi"},
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)

	sizeExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, sizeReq.URL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("executor (size): %v", err)})
		return
	}

	var sizeStdout strings.Builder
	if err := sizeExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: &sizeStdout, Stderr: io.Discard}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("size stream: %v", err)})
		return
	}

	sz := strings.TrimSpace(sizeStdout.String())
	if sz == "" || sz == "MISSING" {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	// Set appropriate content type based on file extension
	if strings.HasSuffix(strings.ToLower(base), ".lz4") {
		c.Writer.Header().Set("Content-Type", "application/x-lz4")
	} else if strings.Contains(strings.ToLower(base), ".tar.") {
		// .tar.gz or .tar.lz4
		if strings.HasSuffix(strings.ToLower(base), ".gz") {
			c.Writer.Header().Set("Content-Type", "application/gzip")
		} else {
			c.Writer.Header().Set("Content-Type", "application/x-lz4")
		}
	} else if strings.HasSuffix(strings.ToLower(base), ".gz") {
		c.Writer.Header().Set("Content-Type", "application/gzip")
	} else {
		c.Writer.Header().Set("Content-Type", "application/octet-stream")
	}

	c.Writer.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", base))
	c.Writer.Header().Set("Content-Length", sz)

	if f, ok := c.Writer.(http.Flusher); ok {
		f.Flush()
	}

	// Stream the file content
	streamReq := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(artifactPod.Name).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: "fileserver",
			Command:   []string{"cat", podPath},
			Stdout:    true,
			Stderr:    true,
		}, kscheme.ParameterCodec)

	streamExec, err := remotecommand.NewSPDYExecutor(restCfg, http.MethodPost, streamReq.URL())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("executor (stream): %v", err)})
		return
	}

	_ = streamExec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: c.Writer, Stderr: io.Discard})
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

func writeJSON(c *gin.Context, status int, v any) {
	c.Header("Cache-Control", "no-store")
	c.IndentedJSON(status, v)
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

func getRESTConfigFromRequest(_ *gin.Context) (*rest.Config, error) {
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
	cfgCopy.Timeout = 30 * time.Minute
	return cfgCopy, nil
}

func getClientFromRequest(c *gin.Context) (client.Client, error) {
	cfg, err := getRESTConfigFromRequest(c)
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

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create k8s client: %w", err)
	}
	return k8sClient, nil
}

func isAuthenticated(c *gin.Context) bool {
	authHeader := c.Request.Header.Get("Authorization")
	token := ""
	token, _ = strings.CutPrefix(authHeader, "Bearer ")
	if token == "" {
		token = c.Request.Header.Get("X-Forwarded-Access-Token")
	}
	if strings.TrimSpace(token) == "" {
		return false
	}
	cfg, err := getRESTConfigFromRequest(c)
	if err != nil {
		return false
	}
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return false
	}
	tr := &authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}
	res, err := clientset.AuthenticationV1().TokenReviews().Create(c.Request.Context(), tr, metav1.CreateOptions{})
	if err != nil {
		return false
	}
	return res.Status.Authenticated
}

func resolveRequester(c *gin.Context) string {
	authHeader := c.Request.Header.Get("Authorization")
	token := ""
	token, _ = strings.CutPrefix(authHeader, "Bearer ")
	if token == "" {
		token = c.Request.Header.Get("X-Forwarded-Access-Token")
	}

	// Attempt TokenReview to obtain canonical username
	if strings.TrimSpace(token) != "" {
		cfg, err := getRESTConfigFromRequest(c)
		if err == nil {
			clientset, err := kubernetes.NewForConfig(cfg)
			if err == nil {
				tr := &authnv1.TokenReview{Spec: authnv1.TokenReviewSpec{Token: token}}
				if res, err := clientset.AuthenticationV1().TokenReviews().Create(c.Request.Context(), tr, metav1.CreateOptions{}); err == nil {
					if res.Status.Authenticated && res.Status.User.Username != "" {
						return res.Status.User.Username
					}
				}
			}
		}
	}

	// Last resort: consult proxy-provided header (not trusted, used only as fallback)
	if u := strings.TrimSpace(c.Request.Header.Get("X-Forwarded-User")); u != "" {
		return u
	}
	return "unknown"
}
