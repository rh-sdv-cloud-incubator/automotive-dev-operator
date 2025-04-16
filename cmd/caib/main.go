package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tektonclientset "github.com/tektoncd/pipeline/pkg/client/clientset/versioned"

	"k8s.io/apimachinery/pkg/api/errors"

	"gopkg.in/yaml.v3"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/apimachinery/pkg/util/wait"

	progressbar "github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/homedir"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automotivev1 "github.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
)

var (
	kubeconfig    string
	namespace     string
	imageBuildCfg string
	manifest      string
	buildName     string
	distro        string
	target        string
	architecture  string
	exportFormat  string
	mode          string
	osbuildImage  string
	storageClass  string
	outputDir     string
	timeout       int
	waitForBuild  bool
	download      bool
	exposeRoute   bool
	customDefs    []string
	followLogs    bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "caib",
		Short: "Cloud Automotive Image Builder",
	}

	buildCmd := &cobra.Command{
		Use:   "build",
		Short: "Create an ImageBuild resource",
		Run:   runBuild,
	}

	downloadCmd := &cobra.Command{
		Use:   "download",
		Short: "Download artifacts from a completed build",
		Run:   runDownload,
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List existing ImageBuilds",
		Run:   runList,
	}

	if home := homedir.HomeDir(); home != "" {
		buildCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
		downloadCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
		listCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
	} else {
		buildCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
		downloadCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
		listCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
	}

	buildCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to create the ImageBuild in")
	buildCmd.Flags().StringVar(&imageBuildCfg, "config", "", "path to ImageBuild YAML configuration file")
	buildCmd.Flags().StringVar(&manifest, "manifest", "", "path to manifest YAML file for the build")
	buildCmd.Flags().StringVar(&buildName, "name", "", "name for the ImageBuild")
	buildCmd.Flags().StringVar(&distro, "distro", "cs9", "distribution to build")
	buildCmd.Flags().StringVar(&target, "target", "qemu", "target platform")
	buildCmd.Flags().StringVar(&architecture, "arch", "arm64", "architecture (amd64, arm64)")
	buildCmd.Flags().StringVar(&exportFormat, "export-format", "image", "export format (image, qcow2)")
	buildCmd.Flags().StringVar(&mode, "mode", "image", "build mode")
	buildCmd.Flags().StringVar(&osbuildImage, "osbuild-image", "quay.io/centos-sig-automotive/automotive-image-builder:latest", "automotive osbuild image")
	buildCmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class for build PVC")
	buildCmd.Flags().IntVar(&timeout, "timeout", 60, "timeout in minutes when waiting for build completion")
	buildCmd.Flags().BoolVarP(&waitForBuild, "wait", "w", false, "wait for the build to complete")
	buildCmd.Flags().BoolVarP(&download, "download", "d", false, "automatically download artifacts when build completes")
	buildCmd.Flags().BoolVarP(&exposeRoute, "route", "r", false, "use a route for downloading artifacts")
	buildCmd.Flags().BoolVarP(&followLogs, "follow", "f", false, "follow logs of the build")
	buildCmd.Flags().StringArrayVar(&customDefs, "define", []string{}, "Custom definition in KEY=VALUE format (can be specified multiple times)")

	downloadCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace where the ImageBuild exists")
	downloadCmd.Flags().StringVar(&buildName, "name", "", "name of the ImageBuild")
	downloadCmd.Flags().StringVar(&outputDir, "output-dir", "./output", "directory to save artifacts")
	downloadCmd.Flags().BoolVarP(&exposeRoute, "route", "r", false, "use a route for downloading artifacts")
	downloadCmd.MarkFlagRequired("name")

	listCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to list ImageBuilds from")

	rootCmd.AddCommand(buildCmd, downloadCmd, listCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runBuild(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	c, err := initializeBuildClient()
	if err != nil {
		handleError(err)
	}

	if err := validateBuildRequirements(); err != nil {
		handleError(err)
	}

	if followLogs {
		waitForBuild = true
	}

	existing := &automotivev1.ImageBuild{}
	err = c.Get(ctx, types.NamespacedName{Name: buildName, Namespace: namespace}, existing)
	if err == nil {
		handleError(fmt.Errorf("ImageBuild %s already exists in namespace %s", buildName, namespace))
	} else if !errors.IsNotFound(err) {
		handleError(fmt.Errorf("error checking if ImageBuild exists: %w", err))
	}

	var configMapName string
	var manifestData []byte

	configMapName, manifestData, err = setupManifestConfigMap(ctx, c, buildName, namespace, manifest)
	if err != nil {
		handleError(err)
	}

	needsCleanup := true
	defer func() {
		if needsCleanup {
			fmt.Printf("Cleaning up ConfigMap %s due to incomplete operation\n", configMapName)
			deleteCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: namespace,
				},
			}

			if delErr := c.Delete(deleteCtx, configMap); delErr != nil {
				fmt.Printf("Warning: Failed to clean up ConfigMap: %v\n", delErr)
			}
		}
	}()

	if err := addCustomDefinitionsToConfigMap(ctx, c, configMapName, namespace, customDefs); err != nil {
		handleError(err)
	}

	imageBuild, err := createImageBuild(ctx, c, buildName, namespace, configMapName, manifestData)
	if err != nil {
		handleError(err)
	}

	if err := updateConfigMapOwnership(ctx, c, configMapName, namespace, imageBuild); err != nil {
		handleError(err)
	}

	needsCleanup = false

	if err := handleLocalFileUploads(ctx, c, namespace, imageBuild, manifestData); err != nil {
		handleError(err)
	}

	fmt.Printf("ImageBuild %s created successfully in namespace %s\n", imageBuild.Name, namespace)

	if followLogs {
		if err := streamBuildLogs(c, imageBuild); err != nil {
			handleError(err)
		}
	} else if waitForBuild {
		if err := handleBuildCompletion(c, imageBuild); err != nil {
			handleError(err)
		}
	}
}

func createImageBuild(ctx context.Context, c client.Client, name, ns, configMapName string, manifestData []byte) (*automotivev1.ImageBuild, error) {
	localFileRefs, err := findLocalFileReferences(string(manifestData))
	if err != nil {
		return nil, fmt.Errorf("error in manifest file references: %w", err)
	}

	labels := map[string]string{
		"app.kubernetes.io/managed-by":                 "caib",
		"app.kubernetes.io/part-of":                    "automotive-dev",
		"app.kubernetes.io/created-by":                 "caib-cli",
		"automotive.sdv.cloud.redhat.com/distro":       distro,
		"automotive.sdv.cloud.redhat.com/target":       target,
		"automotive.sdv.cloud.redhat.com/architecture": architecture,
	}

	serveArtifact := (waitForBuild && download) || exposeRoute

	imageBuild := &automotivev1.ImageBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    labels,
		},
		Spec: automotivev1.ImageBuildSpec{
			Distro:                 distro,
			Target:                 target,
			Architecture:           architecture,
			ExportFormat:           exportFormat,
			Mode:                   mode,
			AutomativeOSBuildImage: osbuildImage,
			StorageClass:           storageClass,
			ServeArtifact:          serveArtifact,
			ServeExpiryHours:       24,
			ManifestConfigMap:      configMapName,
			InputFilesServer:       len(localFileRefs) > 0,
			ExposeRoute:            exposeRoute,
		},
	}

	if err := c.Create(ctx, imageBuild); err != nil {
		return nil, fmt.Errorf("error creating ImageBuild: %w", err)
	}

	if err := updateConfigMapOwnership(ctx, c, configMapName, ns, imageBuild); err != nil {
		return nil, err
	}

	return imageBuild, nil
}

func initializeBuildClient() (client.Client, error) {
	c, err := getClient()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize client: %w", err)
	}
	return c, nil
}

func validateBuildRequirements() error {
	if manifest == "" {
		return fmt.Errorf("--manifest is required")
	}

	if buildName == "" {
		return fmt.Errorf("name is required")
	}

	return nil
}

func handleLocalFileUploads(ctx context.Context, c client.Client, ns string, imageBuild *automotivev1.ImageBuild, manifestData []byte) error {
	if !imageBuild.Spec.InputFilesServer {
		return nil
	}

	localFileRefs, err := findLocalFileReferences(string(manifestData))
	if err != nil {
		return fmt.Errorf("error in manifest file references: %w", err)
	}

	if len(localFileRefs) > 0 {
		return processFileUploads(ctx, c, ns, imageBuild.Name, localFileRefs)
	}
	return nil
}

func setupManifestConfigMap(ctx context.Context, c client.Client, name, ns, manifestPath string) (string, []byte, error) {
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", nil, fmt.Errorf("error reading manifest file: %w", err)
	}

	configMapName := fmt.Sprintf("%s-manifest", name)

	labels := map[string]string{
		"app.kubernetes.io/managed-by":                  "caib",
		"app.kubernetes.io/part-of":                     "automotive-dev",
		"app.kubernetes.io/created-by":                  "caib-cli",
		"automotive.sdv.cloud.redhat.com/resource-type": "manifest-config",
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: ns,
			Labels:    labels,
		},
		Data: map[string]string{
			filepath.Base(manifestPath): string(manifestData),
		},
	}

	fmt.Printf("Creating ConfigMap with manifest file %s\n", manifestPath)
	if err := c.Create(ctx, configMap); err != nil {
		return "", nil, fmt.Errorf("error creating ConfigMap: %w", err)
	}

	return configMap.Name, manifestData, nil
}

func updateConfigMapOwnership(ctx context.Context, c client.Client, configMapName, ns string, imageBuild *automotivev1.ImageBuild) error {
	configMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: ns}, configMap); err != nil {
		return fmt.Errorf("error retrieving ConfigMap for owner update: %w", err)
	}

	configMap.OwnerReferences = []metav1.OwnerReference{
		{
			APIVersion: "automotive.sdv.cloud.redhat.com/v1",
			Kind:       "ImageBuild",
			Name:       imageBuild.Name,
			UID:        imageBuild.UID,
			Controller: ptr.To(true),
		},
	}

	return c.Update(ctx, configMap)
}

func processFileUploads(ctx context.Context, c client.Client, ns, buildName string, localFileRefs []map[string]string) error {
	uploadPod, err := waitForUploadPod(ctx, c, ns, buildName)
	if err != nil {
		return fmt.Errorf("error waiting for upload pod: %w", err)
	}

	fmt.Println("Found local file references in manifest.")
	fmt.Println("Uploading local files to artifact server...")

	if err := uploadLocalFiles(ns, localFileRefs, uploadPod); err != nil {
		return fmt.Errorf("error uploading files: %w", err)
	}

	fmt.Println("Files uploaded successfully.")
	return markUploadsComplete(ctx, c, ns, buildName)
}

func handleBuildCompletion(c client.Client, imageBuild *automotivev1.ImageBuild) error {
	fmt.Printf("Waiting for build %s to complete (timeout: %d minutes)...\n",
		imageBuild.Name, timeout)

	completeBuild, err := waitForBuildCompletion(c, imageBuild.Name,
		imageBuild.Namespace, timeout)
	if err != nil {
		return fmt.Errorf("error waiting for build: %w", err)
	}

	if completeBuild.Status.Phase == "Completed" {
		if exposeRoute {
			fmt.Println("Waiting for artifact URL to be available...")
			completeBuild, err = waitForArtifactURL(c, completeBuild.Name, completeBuild.Namespace)
			if err != nil {
				fmt.Printf("Warning: %v\n", err)
			} else if completeBuild.Status.ArtifactURL != "" {
				artifactFileName := completeBuild.Status.ArtifactFileName
				if artifactFileName == "" {
					var fileExtension string
					switch completeBuild.Spec.ExportFormat {
					case "image":
						fileExtension = ".raw"
					case "qcow2":
						fileExtension = ".qcow2"
					default:
						fileExtension = fmt.Sprintf(".%s", completeBuild.Spec.ExportFormat)
					}
					artifactFileName = fmt.Sprintf("%s-%s%s",
						completeBuild.Spec.Distro,
						completeBuild.Spec.Target,
						fileExtension)
				}

				fmt.Printf("Artifact available for download at: %s\n",
					completeBuild.Status.ArtifactURL+"/workspace/shared/"+artifactFileName)
			}
		}

		if download {
			downloadArtifacts(completeBuild)
		}
	}

	return nil
}

func handleError(err error) {
	fmt.Printf("Error: %v\n", err)
	os.Exit(1)
}

func findLocalFileReferences(manifestContent string) ([]map[string]string, error) {
	var manifestData map[string]any
	var localFiles []map[string]string

	if err := yaml.Unmarshal([]byte(manifestContent), &manifestData); err != nil {
		return nil, fmt.Errorf("failed to parse manifest YAML: %w", err)
	}

	isPathSafe := func(path string) error {
		if path == "" || path == "/" {
			return fmt.Errorf("empty or root path is not allowed")
		}

		if strings.Contains(path, "..") {
			return fmt.Errorf("directory traversal detected in path: %s", path)
		}

		if filepath.IsAbs(path) {
			// TODO add safe dirs flag
			safeDirectories := []string{}
			isInSafeDir := false
			for _, dir := range safeDirectories {
				if strings.HasPrefix(path, dir+"/") {
					isInSafeDir = true
					break
				}
			}
			if !isInSafeDir {
				return fmt.Errorf("absolute path outside safe directories: %s", path)
			}
		}

		return nil
	}

	processAddFiles := func(addFiles []any) error {
		for _, file := range addFiles {
			if fileMap, ok := file.(map[string]any); ok {
				path, hasPath := fileMap["path"].(string)
				sourcePath, hasSourcePath := fileMap["source_path"].(string)
				if hasPath && hasSourcePath {
					if err := isPathSafe(sourcePath); err != nil {
						return err
					}
					localFiles = append(localFiles, map[string]string{
						"path":        path,
						"source_path": sourcePath,
					})
				}
			}
		}
		return nil
	}

	if content, ok := manifestData["content"].(map[string]any); ok {
		if addFiles, ok := content["add_files"].([]any); ok {
			if err := processAddFiles(addFiles); err != nil {
				return nil, err
			}
		}
	}

	if qm, ok := manifestData["qm"].(map[string]any); ok {
		if qmContent, ok := qm["content"].(map[string]any); ok {
			if addFiles, ok := qmContent["add_files"].([]any); ok {
				if err := processAddFiles(addFiles); err != nil {
					return nil, err
				}
			}
		}
	}

	return localFiles, nil
}

func uploadLocalFiles(namespace string, files []map[string]string, uploadPod *corev1.Pod) error {
	config, err := getRESTConfig()
	if err != nil {
		return fmt.Errorf("unable to get REST config: %w", err)
	}

	fmt.Printf("uploading %d files to build pod\n", len(files))

	for _, fileRef := range files {
		sourcePath := fileRef["source_path"]
		destPath := fileRef["source_path"]

		destDir := filepath.Dir(destPath)
		if destDir != "." && destDir != "/" {
			mkdirCmd := []string{"/bin/sh", "-c", fmt.Sprintf("mkdir -p /workspace/shared/%s", destDir)}
			if err := execInPod(config, namespace, uploadPod.Name, uploadPod.Spec.Containers[0].Name, mkdirCmd); err != nil {
				return fmt.Errorf("error creating directory structure: %w", err)
			}
		}

		fmt.Printf("Copying %s to pod:/workspace/shared/%s\n", sourcePath, destPath)
		if err := copyFile(config, namespace, uploadPod.Name, uploadPod.Spec.Containers[0].Name, sourcePath, "/workspace/shared/"+destPath, true); err != nil {
			return fmt.Errorf("error copying file %s: %w", sourcePath, err)
		}
	}

	return nil
}

func copyFile(config *rest.Config, namespace, podName, containerName, localPath, podPath string, toPod bool) error {
	configCopy := rest.CopyConfig(config)

	if !toPod {
		configCopy.Timeout = 30 * time.Minute
	}

	clientset, err := kubernetes.NewForConfig(configCopy)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	if toPod {
		// Upload code remains largely the same
		destDir := filepath.Dir(podPath)
		mkdirCmd := []string{"mkdir", "-p", destDir}
		if err := execInPod(configCopy, namespace, podName, containerName, mkdirCmd); err != nil {
			return fmt.Errorf("error creating destination directory: %w", err)
		}

		file, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("error opening local file: %w", err)
		}
		defer file.Close()

		stat, err := file.Stat()
		if err != nil {
			return fmt.Errorf("error getting file stats: %w", err)
		}

		bar := progressbar.DefaultBytes(
			stat.Size(),
			"Uploading",
		)

		pipeReader, pipeWriter := io.Pipe()

		go func() {
			tarWriter := tar.NewWriter(pipeWriter)
			defer func() {
				tarWriter.Close()
				pipeWriter.Close()
			}()

			header := &tar.Header{
				Name:    filepath.Base(podPath),
				Size:    stat.Size(),
				Mode:    int64(stat.Mode()),
				ModTime: stat.ModTime(),
			}

			if err := tarWriter.WriteHeader(header); err != nil {
				pipeWriter.CloseWithError(fmt.Errorf("error writing tar header: %w", err))
				return
			}

			buf := make([]byte, 4*1024*1024) // 4MB chunks
			for {
				n, err := file.Read(buf)
				if err != nil && err != io.EOF {
					pipeWriter.CloseWithError(fmt.Errorf("error reading file: %w", err))
					return
				}
				if n == 0 {
					break
				}

				if _, err := tarWriter.Write(buf[:n]); err != nil {
					pipeWriter.CloseWithError(fmt.Errorf("error writing to tar: %w", err))
					return
				}

				bar.Add(n)
			}
		}()

		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(podName).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: containerName,
				Command:   []string{"tar", "-xf", "-", "-C", filepath.Dir(podPath)},
				Stdin:     true,
				Stdout:    true,
				Stderr:    true,
			}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(configCopy, "POST", req.URL())
		if err != nil {
			return fmt.Errorf("error creating SPDY executor: %w", err)
		}

		var stdout, stderr bytes.Buffer
		err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdin:  pipeReader,
			Stdout: &stdout,
			Stderr: &stderr,
		})

		if err != nil {
			return fmt.Errorf("exec error: %v, stderr: %s", err, stderr.String())
		}
	} else {
		sizeCmd := []string{"stat", "-c", "%s", podPath}
		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(podName).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: containerName,
				Command:   sizeCmd,
				Stdout:    true,
				Stderr:    true,
			}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(configCopy, "POST", req.URL())
		if err != nil {
			return fmt.Errorf("error creating SPDY executor: %w", err)
		}

		var sizeStdout, sizeStderr bytes.Buffer
		err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdout: &sizeStdout,
			Stderr: &sizeStderr,
		})

		if err != nil {
			return fmt.Errorf("error checking file: %v, stderr: %s", err, sizeStderr.String())
		}

		fileSize, err := strconv.ParseInt(strings.TrimSpace(sizeStdout.String()), 10, 64)
		if err != nil {
			return fmt.Errorf("error parsing file size: %w", err)
		}

		tempFile := localPath + ".download"
		outFile, err := os.Create(tempFile)
		if err != nil {
			return fmt.Errorf("error creating local file: %w", err)
		}
		defer func() {
			outFile.Close()
			if err != nil {
				os.Remove(tempFile)
			}
		}()

		bar := progressbar.DefaultBytes(
			fileSize,
			"Downloading",
		)

		bufWriter := bufio.NewWriterSize(outFile, 8*1024*1024) // 8MB buffer
		writer := io.MultiWriter(bufWriter, bar)

		cmd := []string{"cat", podPath}
		req = clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(podName).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: containerName,
				Command:   cmd,
				Stdout:    true,
				Stderr:    true,
			}, scheme.ParameterCodec)

		exec, err = remotecommand.NewSPDYExecutor(configCopy, "POST", req.URL())
		if err != nil {
			return fmt.Errorf("error creating SPDY executor for download: %w", err)
		}

		var stderr bytes.Buffer
		err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdout: writer,
			Stderr: &stderr,
		})

		if flushErr := bufWriter.Flush(); flushErr != nil {
			return fmt.Errorf("error flushing output buffer: %w", flushErr)
		}

		if err != nil {
			return fmt.Errorf("exec error during download: %v, stderr: %s", err, stderr.String())
		}

		outFile.Close()

		if info, err := os.Stat(tempFile); err == nil {
			if info.Size() != fileSize {
				return fmt.Errorf("incomplete download: got %d bytes, expected %d bytes",
					info.Size(), fileSize)
			}

			if err := os.Rename(tempFile, localPath); err != nil {
				return fmt.Errorf("error moving completed download to final location: %w", err)
			}
		} else {
			return fmt.Errorf("error getting stats of downloaded file: %w", err)
		}
	}

	fmt.Println()
	return nil
}

func downloadArtifacts(imageBuild *automotivev1.ImageBuild) {
	if outputDir == "" {
		outputDir = "./output"
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("Error creating output directory: %v\n", err)
		return
	}

	artifactFileName := imageBuild.Status.ArtifactFileName
	if artifactFileName == "" {
		var fileExtension string
		switch imageBuild.Spec.ExportFormat {
		case "image":
			fileExtension = ".raw"
		case "qcow2":
			fileExtension = ".qcow2"
		default:
			fileExtension = fmt.Sprintf(".%s", imageBuild.Spec.ExportFormat)
		}
		artifactFileName = fmt.Sprintf("%s-%s%s",
			imageBuild.Spec.Distro,
			imageBuild.Spec.Target,
			fileExtension)
	}

	ctx := context.Background()
	c, err := getClient()
	if err != nil {
		fmt.Printf("Error creating client: %v\n", err)
		return
	}

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Cap:      60 * time.Second,
	}

	var artifactPod *corev1.Pod
	findPodErr := wait.ExponentialBackoff(backoff, func() (bool, error) {
		podList := &corev1.PodList{}
		if err := c.List(ctx, podList,
			client.InNamespace(imageBuild.Namespace),
			client.MatchingLabels{
				"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
				"app.kubernetes.io/name":                          "artifact-pod",
			}); err != nil {
			fmt.Printf("Error listing pods (will retry): %v\n", err)
			return false, nil
		}

		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Status.Phase == corev1.PodRunning {
				for _, status := range pod.Status.ContainerStatuses {
					if status.Name == "fileserver" && status.Ready {
						artifactPod = pod
						return true, nil
					}
				}
			}
		}

		fmt.Println("No ready artifact pod found yet, waiting...")
		return false, nil
	})

	if findPodErr != nil {
		fmt.Printf("Failed to find ready artifact pod: %v\n", findPodErr)
		return
	}

	containerName := "fileserver"
	sourcePath := "/workspace/shared/" + artifactFileName
	outputPath := filepath.Join(outputDir, artifactFileName)

	fmt.Printf("Downloading artifact from pod %s\n", artifactPod.Name)
	fmt.Printf("Pod path: %s\n", sourcePath)
	fmt.Printf("Saving to: %s\n", outputPath)

	downloadBackoff := wait.Backoff{
		Steps:    3,
		Duration: 5 * time.Second,
		Factor:   2.0,
		Jitter:   0.1,
		Cap:      30 * time.Second,
	}

	downloadErr := wait.ExponentialBackoff(downloadBackoff, func() (bool, error) {
		freshConfig, err := getRESTConfig()
		if err != nil {
			fmt.Printf("Failed to get REST config (will retry): %v\n", err)
			return false, nil
		}

		err = copyFile(freshConfig, imageBuild.Namespace, artifactPod.Name, containerName, outputPath, sourcePath, false)
		if err != nil {
			fmt.Printf("Download attempt failed (will retry): %v\n", err)
			return false, nil
		}
		return true, nil
	})

	if downloadErr != nil {
		fmt.Printf("Failed to download artifact after multiple retries: %v\n", downloadErr)
		if _, err := os.Stat(outputPath); err == nil {
			os.Remove(outputPath)
			fmt.Println("Removed incomplete download file")
		}
		return
	}

	if fileInfo, err := os.Stat(outputPath); err == nil {
		fileSizeMB := float64(fileInfo.Size()) / 1024 / 1024
		fmt.Printf("Artifact downloaded successfully to %s (%.2f MB)\n", outputPath, fileSizeMB)
	} else {
		fmt.Printf("Artifact downloaded but unable to get file size: %v\n", err)
	}
}

func getRESTConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("error building config: %w", err)
		}
	}

	configCopy := rest.CopyConfig(config)
	configCopy.Timeout = time.Minute * 10

	return configCopy, nil
}

func waitForBuildCompletion(c client.Client, name, namespace string, timeoutMinutes int) (*automotivev1.ImageBuild, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	var completedBuild *automotivev1.ImageBuild
	var lastPhase, lastMessage string

	err := wait.PollUntilContextTimeout(
		ctx,
		10*time.Second,
		time.Duration(timeoutMinutes)*time.Minute,
		false,
		func(ctx context.Context) (bool, error) {
			imageBuild := &automotivev1.ImageBuild{}
			if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, imageBuild); err != nil {
				return false, err
			}

			completedBuild = imageBuild

			if imageBuild.Status.Phase == "Completed" {
				if imageBuild.Status.Phase != lastPhase || imageBuild.Status.Message != lastMessage {
					fmt.Printf("\nstatus: %s - %s\n", imageBuild.Status.Phase, imageBuild.Status.Message)
				}
				return true, nil
			}

			if imageBuild.Status.Phase == "Failed" {
				fmt.Println()
				return false, fmt.Errorf("build failed: %s", imageBuild.Status.Message)
			}

			if imageBuild.Status.Phase != lastPhase || imageBuild.Status.Message != lastMessage {
				fmt.Printf("\nstatus: %s - %s\n", imageBuild.Status.Phase, imageBuild.Status.Message)
				lastPhase = imageBuild.Status.Phase
				lastMessage = imageBuild.Status.Message
			} else {
				fmt.Print(".")
			}

			return false, nil
		})

	fmt.Println()
	return completedBuild, err
}

func runDownload(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	c, err := getClient()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	imageBuild := &automotivev1.ImageBuild{}
	if err := c.Get(ctx, types.NamespacedName{Name: buildName, Namespace: namespace}, imageBuild); err != nil {
		fmt.Printf("Error getting ImageBuild %s: %v\n", buildName, err)
		os.Exit(1)
	}

	if imageBuild.Status.Phase != "Completed" {
		fmt.Printf("Build %s is not completed (status: %s). Cannot download artifacts.\n", buildName, imageBuild.Status.Phase)
		os.Exit(1)
	}

	if exposeRoute || cmd.Flags().Changed("route") {
		exposedURL := imageBuild.Status.ArtifactURL
		if exposedURL == "" {
			fmt.Printf("No ArtifactURL found in ImageBuild status for %s\n", buildName)
			os.Exit(1)
		}

		artifactFileName := imageBuild.Status.ArtifactFileName
		if artifactFileName == "" {
			var fileExtension string
			switch imageBuild.Spec.ExportFormat {
			case "image":
				fileExtension = ".raw"
			case "qcow2":
				fileExtension = ".qcow2"
			default:
				fileExtension = fmt.Sprintf(".%s", imageBuild.Spec.ExportFormat)
			}
			artifactFileName = fmt.Sprintf("%s-%s%s",
				imageBuild.Spec.Distro,
				imageBuild.Spec.Target,
				fileExtension)
		}

		fmt.Printf("Artifact available for download at: %s\n",
			exposedURL+"/workspace/shared/"+artifactFileName)

		if cmd.Flags().Changed("route") && !cmd.Flags().Changed("output-dir") {
			return
		}
	}

	downloadArtifacts(imageBuild)
}

func runList(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	c, err := getClient()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	builds := &automotivev1.ImageBuildList{}
	if err := c.List(ctx, builds, client.InNamespace(namespace)); err != nil {
		fmt.Printf("Error listing ImageBuilds: %v\n", err)
		os.Exit(1)
	}

	if len(builds.Items) == 0 {
		fmt.Printf("No ImageBuilds found in namespace %s\n", namespace)
		return
	}

	fmt.Printf("%-20s %-12s %-20s %-20s %-10s\n", "NAME", "STATUS", "DISTRO", "TARGET", "CREATED")
	for _, build := range builds.Items {
		createdTime := build.CreationTimestamp.Format("2006-01-02 15:04")
		fmt.Printf("%-20s %-12s %-20s %-20s %-10s\n",
			build.Name,
			build.Status.Phase,
			build.Spec.Distro,
			build.Spec.Target,
			createdTime)
	}
}

func getClient() (client.Client, error) {
	ctrl.SetLogger(logr.Discard())

	var config *rest.Config
	var err error

	config, err = rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("error building config: %w", err)
		}
	}

	scheme := runtime.NewScheme()
	if err := automotivev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("error adding automotive scheme: %w", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("error adding core scheme: %w", err)
	}

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("error creating Kubernetes client: %w", err)
	}

	return c, nil
}

func execInPod(config *rest.Config, namespace, podName, containerName string, command []string) error {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("error creating SPDY executor: %w", err)
	}

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		return fmt.Errorf("exec error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

func markUploadsComplete(ctx context.Context, c client.Client, namespace, buildName string) error {
	original := &automotivev1.ImageBuild{}
	if err := c.Get(ctx, types.NamespacedName{Name: buildName, Namespace: namespace}, original); err != nil {
		return fmt.Errorf("error getting ImageBuild: %w", err)
	}

	patched := original.DeepCopy()
	if patched.Annotations == nil {
		patched.Annotations = make(map[string]string)
	}
	patched.Annotations["automotive.sdv.cloud.redhat.com/uploads-complete"] = "true"

	if err := c.Patch(ctx, patched, client.MergeFrom(original)); err != nil {
		return fmt.Errorf("error patching ImageBuild with completion annotation: %w", err)
	}

	fmt.Println("File uploads marked as complete. Build will proceed.")
	return nil
}

func waitForUploadPod(ctx context.Context, c client.Client, namespace, buildName string) (*corev1.Pod, error) {
	fmt.Println("Waiting for file upload server to be ready...")

	var uploadPod *corev1.Pod
	err := wait.PollUntilContextTimeout(
		ctx,
		5*time.Second,
		2*time.Minute,
		false,
		func(ctx context.Context) (bool, error) {
			podList := &corev1.PodList{}
			if err := c.List(ctx, podList,
				client.InNamespace(namespace),
				client.MatchingLabels{
					"automotive.sdv.cloud.redhat.com/imagebuild-name": buildName,
					"app.kubernetes.io/name":                          "upload-pod",
				}); err != nil {
				return false, err
			}

			for _, pod := range podList.Items {
				if pod.Status.Phase == corev1.PodRunning {
					uploadPod = &pod
					return true, nil
				}
			}
			fmt.Print(".")
			return false, nil
		})

	if err != nil {
		return nil, fmt.Errorf("timed out waiting for upload pod: %w", err)
	}

	fmt.Printf("\nUpload pod is ready: %s\n", uploadPod.Name)
	return uploadPod, nil
}

func addCustomDefinitionsToConfigMap(ctx context.Context, c client.Client, configMapName, ns string, defs []string) error {
	if len(defs) == 0 {
		return nil
	}

	for _, def := range defs {
		if !strings.Contains(def, "=") {
			return fmt.Errorf("invalid custom definition format (must be KEY=VALUE): %s", def)
		}
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: ns}, cm); err != nil {
		return fmt.Errorf("error getting ConfigMap for custom definitions: %w", err)
	}

	defsContent := strings.Join(defs, " ")
	fmt.Printf("adding custom definitions to ConfigMap: %s\n", defsContent)

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data["custom-definitions.env"] = defsContent

	if err := c.Update(ctx, cm); err != nil {
		return fmt.Errorf("error updating ConfigMap with custom definitions: %w", err)
	}

	return nil
}

func waitForArtifactURL(c client.Client, name, namespace string) (*automotivev1.ImageBuild, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	return waitForResource(ctx, 5*time.Second, 2*time.Minute, "waiting for artifact URL",
		func(ctx context.Context) (*automotivev1.ImageBuild, bool, error) {
			build := &automotivev1.ImageBuild{}
			if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, build); err != nil {
				return nil, false, err
			}

			return build, build.Status.ArtifactURL != "", nil
		})
}

func waitForResource[T any](ctx context.Context, interval, timeout time.Duration, message string, checkFn func(ctx context.Context) (T, bool, error)) (T, error) {
	var result T
	var zero T

	err := wait.PollUntilContextTimeout(
		ctx,
		interval,
		timeout,
		false,
		func(ctx context.Context) (bool, error) {
			var done bool
			var err error

			result, done, err = checkFn(ctx)
			if err != nil {
				return false, err
			}

			if !done && message != "" {
				fmt.Print(".")
			}

			return done, nil
		})

	if message != "" {
		fmt.Println()
	}

	if err != nil {
		return zero, fmt.Errorf("%s: %w", message, err)
	}

	return result, nil
}

func streamBuildLogs(c client.Client, imageBuild *automotivev1.ImageBuild) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Minute)
	defer cancel()

	fmt.Printf("Waiting for build %s to start and streaming logs...\n", imageBuild.Name)

	taskRunName, err := waitForResource(ctx, 2*time.Second, 2*time.Minute, "",
		func(ctx context.Context) (string, bool, error) {
			ib := &automotivev1.ImageBuild{}
			if err := c.Get(ctx, types.NamespacedName{Name: imageBuild.Name, Namespace: imageBuild.Namespace}, ib); err != nil {
				return "", false, err
			}

			return ib.Status.TaskRunName, ib.Status.TaskRunName != "", nil
		})

	if err != nil {
		return fmt.Errorf("error waiting for build TaskRun to be created: %w", err)
	}

	fmt.Printf("Build started with TaskRun: %s\n", taskRunName)

	tektonClient, err := getTektonClient()
	if err != nil {
		return fmt.Errorf("failed to create Tekton client: %w", err)
	}

	podName, err := waitForResource(ctx, 2*time.Second, 2*time.Minute, "",
		func(ctx context.Context) (string, bool, error) {
			tr, err := tektonClient.TektonV1().TaskRuns(imageBuild.Namespace).Get(ctx, taskRunName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return "", false, nil
				}
				return "", false, err
			}

			return tr.Status.PodName, tr.Status.PodName != "", nil
		})

	if err != nil {
		return fmt.Errorf("error waiting for build pod: %w", err)
	}

	config, err := getRESTConfig()
	if err != nil {
		return fmt.Errorf("failed to get REST config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	fmt.Println("Waiting for build pod to be ready...")

	_, err = waitForResource(ctx, 2*time.Second, 2*time.Minute, "",
		func(ctx context.Context) (*corev1.Pod, bool, error) {
			pod, err := kubeClient.CoreV1().Pods(imageBuild.Namespace).Get(ctx, podName, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return nil, false, nil
				}
				return nil, false, err
			}

			return pod, pod.Status.Phase == corev1.PodRunning, nil
		})

	if err != nil {
		return fmt.Errorf("error waiting for build pod to start running: %w", err)
	}

	pod, err := kubeClient.CoreV1().Pods(imageBuild.Namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting pod details: %w", err)
	}

	var stepContainers []string
	for _, container := range pod.Spec.Containers {
		if strings.HasPrefix(container.Name, "step-") {
			stepContainers = append(stepContainers, container.Name)
		}
	}

	if len(stepContainers) == 0 {
		fmt.Println("Warning: No step containers found in pod")
		stepContainers = []string{
			"step-find-manifest-file",
			"step-build-image",
		}
	}

	fmt.Printf("Found %d step containers\n", len(stepContainers))

	for _, containerName := range stepContainers {
		fmt.Printf("Waiting for %s container to start...\n", containerName)

		_, err = waitForResource(ctx, 2*time.Second, 30*time.Second, "",
			func(ctx context.Context) (*corev1.Pod, bool, error) {
				pod, err := kubeClient.CoreV1().Pods(imageBuild.Namespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					return nil, false, err
				}

				for _, status := range pod.Status.ContainerStatuses {
					if status.Name == containerName && (status.State.Running != nil || status.State.Terminated != nil) {
						return pod, true, nil
					}
				}

				return pod, false, nil
			})

		if err != nil {
			fmt.Printf("Warning: Container %s not ready in time, trying to get logs anyway\n", containerName)
		}

		if err := streamContainerLogs(ctx, kubeClient, imageBuild.Namespace, podName, containerName); err != nil {
			fmt.Printf("Warning: %v\n", err)
		}
	}

	return handleBuildCompletion(c, imageBuild)
}

func streamContainerLogs(ctx context.Context, kubeClient *kubernetes.Clientset, namespace, podName, containerName string) error {
	req := kubeClient.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		Follow:    true,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("couldn't stream logs from %s container: %v", containerName, err)
	}
	defer stream.Close()

	stepName := containerName
	if strings.HasPrefix(containerName, "step-") {
		stepName = strings.TrimPrefix(containerName, "step-")
	}

	fmt.Printf("\n===== Logs from %s =====\n\n", stepName)

	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		fmt.Println(scanner.Text())
	}

	if scanner.Err() != nil && ctx.Err() == nil {
		return fmt.Errorf("error reading logs: %v", scanner.Err())
	}

	return nil
}

func getTektonClient() (*tektonclientset.Clientset, error) {
	config, err := getRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get REST config: %w", err)
	}

	tektonClient, err := tektonclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Tekton client: %w", err)
	}

	return tektonClient, nil
}
