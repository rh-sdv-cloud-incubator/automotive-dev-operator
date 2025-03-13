package main

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"crypto/tls"
	"io"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/util/wait"

	progressbar "github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
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
	kubeconfig       string
	namespace        string
	imageBuildCfg    string
	manifest         string
	buildName        string
	distro           string
	target           string
	architecture     string
	exportFormat     string
	mode             string
	osbuildImage     string
	storageClass     string
	outputDir        string
	timeout          int
	waitForBuild     bool
	download         bool
	handleLocalFiles bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "automotive-cli",
		Short: "Command-line tool for Automotive Developer Operator",
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

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show details of a specific ImageBuild",
		Args:  cobra.ExactArgs(1),
		Run:   runShow,
	}

	if home := homedir.HomeDir(); home != "" {
		buildCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
		downloadCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
		listCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
		showCmd.Flags().StringVar(&kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "path to the kubeconfig file")
	} else {
		buildCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
		downloadCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
		listCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
		showCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to the kubeconfig file")
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
	buildCmd.Flags().StringVar(&osbuildImage, "osbuild-image", "quay.io/centos-sig-automotive/automotive-osbuild:latest", "automotive osbuild image")
	buildCmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class for build PVC")
	buildCmd.Flags().IntVar(&timeout, "timeout", 60, "timeout in minutes when waiting for build completion")
	buildCmd.Flags().BoolVarP(&waitForBuild, "wait", "w", false, "wait for the build to complete")
	buildCmd.Flags().BoolVarP(&download, "download", "d", false, "automatically download artifacts when build completes")
	buildCmd.Flags().BoolVar(&handleLocalFiles, "handle-local-files", true, "Handle local files referenced in manifest (upload and serve)")

	downloadCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace where the ImageBuild exists")
	downloadCmd.Flags().StringVar(&buildName, "name", "", "name of the ImageBuild")
	downloadCmd.Flags().StringVar(&outputDir, "output-dir", "./output", "directory to save artifacts")
	downloadCmd.MarkFlagRequired("name")

	listCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace to list ImageBuilds from")

	showCmd.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace of the ImageBuild")

	rootCmd.AddCommand(buildCmd, downloadCmd, listCmd, showCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func runBuild(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	c, err := getClient()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Create a new ImageBuild every time
	// TODO this creates a race condition with the deletion of dependant resources
	if buildName == "" {
		fmt.Println("Error: --name flag is required")
		os.Exit(1)
	}

	existingIB := &automotivev1.ImageBuild{}
	err = c.Get(ctx, types.NamespacedName{Name: buildName, Namespace: namespace}, existingIB)
	if err == nil {
		fmt.Printf("Deleting existing ImageBuild %s\n", buildName)
		if err := c.Delete(ctx, existingIB); err != nil {
			fmt.Printf("Error deleting existing ImageBuild: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Waiting for ImageBuild %s to be deleted...\n", buildName)
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			err := c.Get(ctx, types.NamespacedName{Name: buildName, Namespace: namespace}, &automotivev1.ImageBuild{})
			return errors.IsNotFound(err), nil
		})
		if err != nil {
			fmt.Printf("Error waiting for ImageBuild deletion: %v\n", err)
			os.Exit(1)
		}
	} else if !errors.IsNotFound(err) {
		fmt.Printf("Error checking for existing ImageBuild: %v\n", err)
		os.Exit(1)
	}

	imageBuild := &automotivev1.ImageBuild{
		ObjectMeta: metav1.ObjectMeta{
			Name:      buildName,
			Namespace: namespace,
		},
		Spec: automotivev1.ImageBuildSpec{
			Distro:                 distro,
			Target:                 target,
			Architecture:           architecture,
			ExportFormat:           exportFormat,
			Mode:                   mode,
			AutomativeOSBuildImage: osbuildImage,
			StorageClass:           storageClass,
			ServeArtifact:          waitForBuild && download,
			ServeExpiryHours:       24,
		},
	}

	if manifest == "" {
		fmt.Println("Error: --manifest is required")
		os.Exit(1)
	}

	configMapName := fmt.Sprintf("%s-manifest-config", imageBuild.Name)
	imageBuild.Spec.ManifestConfigMap = configMapName

	fmt.Printf("Creating ImageBuild %s\n", imageBuild.Name)
	if err := c.Create(ctx, imageBuild); err != nil {
		fmt.Printf("Error creating ImageBuild: %v\n", err)
		os.Exit(1)
	}

	manifestData, err := os.ReadFile(manifest)
	if err != nil {
		fmt.Printf("Error reading manifest file: %v\n", err)
		os.Exit(1)
	}

	var inputFilesPVC string

	if handleLocalFiles {
		localFileRefs := findLocalFileReferences(string(manifestData))

		if len(localFileRefs) > 0 {
			fmt.Println("Found local file references in manifest.")

			inputFilesPVC = fmt.Sprintf("%s-input-files", buildName)
			if err := createPVC(ctx, c, inputFilesPVC, namespace, imageBuild.Name, imageBuild.UID); err != nil {
				fmt.Printf("Error creating input files PVC: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("Uploading local files with path preservation...")
			if err := uploadLocalFilesWithPathPreservation(ctx, c, namespace, inputFilesPVC, localFileRefs); err != nil {
				fmt.Printf("Error uploading files: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("Files uploaded successfully - original paths preserved.")

			imageBuild.Spec.InputFilesPVC = inputFilesPVC

			if err := c.Update(ctx, imageBuild); err != nil {
				fmt.Printf("Error updating ImageBuild with InputFilesPVC: %v\n", err)
				os.Exit(1)
			}
		}
	}

	fileName := filepath.Base(manifest)
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "automotive.sdv.cloud.redhat.com/v1",
					Kind:       "ImageBuild",
					Name:       imageBuild.Name,
					UID:        imageBuild.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Data: map[string]string{
			fileName: string(manifestData),
		},
	}

	existingCM := &corev1.ConfigMap{}
	err = c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, existingCM)
	if err == nil {
		fmt.Printf("Deleting existing ConfigMap %s\n", configMapName)
		if err := c.Delete(ctx, existingCM); err != nil {
			fmt.Printf("Error deleting ConfigMap: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Waiting for ConfigMap %s to be deleted...\n", configMapName)
		err = wait.PollUntilContextTimeout(ctx, 2*time.Second, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			err := c.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: namespace}, &corev1.ConfigMap{})
			return errors.IsNotFound(err), nil
		})
		if err != nil {
			fmt.Printf("Error waiting for ConfigMap deletion: %v\n", err)
			os.Exit(1)
		}
	} else if !errors.IsNotFound(err) {
		fmt.Printf("Error checking for existing ConfigMap: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Creating ConfigMap %s\n", configMapName)
	if err := c.Create(ctx, configMap); err != nil {
		fmt.Printf("Error creating ConfigMap: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("ImageBuild %s created successfully in namespace %s\n", imageBuild.Name, namespace)

	if waitForBuild {
		fmt.Printf("Waiting for build %s to complete (timeout: %d minutes)...\n", imageBuild.Name, timeout)
		var completeBuild *automotivev1.ImageBuild
		if completeBuild, err = waitForBuildCompletion(c, imageBuild.Name, namespace, timeout); err != nil {
			fmt.Printf("Error waiting for build: %v\n", err)
			os.Exit(1)
		}

		if download && completeBuild.Status.Phase == "Completed" {
			downloadArtifacts(completeBuild)
		}
	}
}

// findLocalFileReferences parses the manifest to find local file references
func findLocalFileReferences(manifestContent string) []map[string]string {
	var localFiles []map[string]string

	// Use a proper YAML parser to find add_files sections
	// This is a simplified example using regexp
	re := regexp.MustCompile(`add_files:\s*- path:\s*([^\n]+)\s*source_path:\s*([^\n]+)`)
	matches := re.FindAllStringSubmatch(manifestContent, -1)

	for _, match := range matches {
		if len(match) == 3 {
			path := strings.TrimSpace(match[1])
			sourcePath := strings.TrimSpace(match[2])

			// Check if it's a local file (not a URL)
			if !strings.HasPrefix(sourcePath, "http://") &&
				!strings.HasPrefix(sourcePath, "https://") {
				localFiles = append(localFiles, map[string]string{
					"path":        path,
					"source_path": sourcePath,
				})
			}
		}
	}

	return localFiles
}

func uploadLocalFilesWithPathPreservation(ctx context.Context, c client.Client, namespace, pvcName string,
	files []map[string]string) error {
	// Create a helper pod that mounts the PVC
	helperPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "file-upload-helper-",
			Namespace:    namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-cli",
				"app.kubernetes.io/purpose":    "file-upload",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "helper",
					Image:   "registry.access.redhat.com/ubi8/ubi-minimal:latest",
					Command: []string{"sleep", "600"}, // Keep running for uploads
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "storage",
							MountPath: "/data",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "storage",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: pvcName,
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	if err := c.Create(ctx, helperPod); err != nil {
		return fmt.Errorf("failed to create helper pod: %w", err)
	}

	// Schedule pod for deletion when we're done
	defer func() {
		if err := c.Delete(ctx, helperPod); err != nil {
			fmt.Printf("Warning: Failed to delete helper pod: %v\n", err)
		}
	}()

	// Wait for pod to be ready
	podReady := false
	for i := 0; i < 30; i++ { // Wait up to 30 seconds
		pod := &corev1.Pod{}
		if err := c.Get(ctx, client.ObjectKey{Name: helperPod.Name, Namespace: namespace}, pod); err != nil {
			return fmt.Errorf("failed to get pod: %w", err)
		}

		if pod.Status.Phase == corev1.PodRunning {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					podReady = true
					break
				}
			}
		}

		if podReady {
			break
		}

		time.Sleep(1 * time.Second)
	}

	if !podReady {
		return fmt.Errorf("helper pod did not become ready in time")
	}

	// Get REST config for file uploads
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return fmt.Errorf("error building config: %w", err)
		}
	}

	// Upload each file
	for _, fileRef := range files {
		sourcePath := fileRef["source_path"]
		// Create complete path in PVC matching original path
		// Mount source_path, not the path field - the manifest references the source_path
		destPath := "/data" + sourcePath

		// Ensure directories exist
		dirPath := filepath.Dir(destPath)
		execCmd := []string{"mkdir", "-p", dirPath}

		fmt.Printf("Creating directory: %s\n", dirPath)
		if err := execInPod(config, namespace, helperPod.Name, "helper", execCmd); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dirPath, err)
		}

		// Copy file to the same path
		fmt.Printf("Uploading %s to match original path\n", sourcePath)
		if err := copyFileToPod(config, namespace, helperPod.Name, "helper",
			sourcePath, destPath); err != nil {
			return fmt.Errorf("failed to copy file %s: %w", sourcePath, err)
		}
	}

	return nil
}

// copyFileToPod copies a local file to a pod
func copyFileToPod(config *rest.Config, namespace, podName, containerName,
	srcPath, destPath string) error {
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	// Create an in-memory tar archive
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Get file info
	fileInfo, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("error accessing file %s: %w", srcPath, err)
	}

	// Open the source file
	file, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("error opening file %s: %w", srcPath, err)
	}
	defer file.Close()

	// Create tar header
	header := &tar.Header{
		Name: filepath.Base(destPath),
		Size: fileInfo.Size(),
		Mode: int64(fileInfo.Mode()),
	}

	// Write tar header
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("error writing tar header: %w", err)
	}

	// Copy file data to tar
	if _, err := io.Copy(tw, file); err != nil {
		return fmt.Errorf("error copying file data to tar: %w", err)
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("error closing tar writer: %w", err)
	}

	// Prepare extract command - this extracts the tar stream from stdin
	cmd := []string{"tar", "-xf", "-", "-C", filepath.Dir(destPath)}

	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	// Create SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return fmt.Errorf("error creating SPDY executor: %w", err)
	}

	// Execute and stream the tar data
	var stdout, stderr bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  &buf,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		return fmt.Errorf("exec error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}

func createPVC(ctx context.Context, c client.Client, pvcName, namespace string,
	ownerName string, ownerUID types.UID) error {
	storageSize := resource.MustParse("1Gi")

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-cli",
				"app.kubernetes.io/part-of":    ownerName,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "automotive.sdv.cloud.redhat.com/v1",
					Kind:       "ImageBuild",
					Name:       ownerName,
					UID:        ownerUID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
		},
	}

	if storageClass != "" {
		pvc.Spec.StorageClassName = &storageClass
	}

	// Create the PVC
	if err := c.Create(ctx, pvc); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create input files PVC: %w", err)
		}
	}

	return nil
}

func rewriteManifestWithServerURLs(manifestContent, serverURL string,
	localFiles []map[string]string) string {
	// For each local file reference, replace with URL
	result := manifestContent
	for _, fileRef := range localFiles {
		localPath := fileRef["source_path"]
		serverPath := serverURL + "/" + fileRef["path"]

		// Replace the source_path in the manifest
		// A more robust implementation would use a proper YAML parser
		result = strings.Replace(
			result,
			"source_path: "+localPath,
			"source_path: "+serverPath,
			-1,
		)
	}

	return result
}

func downloadArtifacts(imageBuild *automotivev1.ImageBuild) {
	if imageBuild.Status.ArtifactURL == "" {
		fmt.Println("No artifact URL is available. Cannot download artifacts.")
		return
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		fmt.Printf("Error creating output directory: %v\n", err)
		return
	}

	artifactURL := fmt.Sprintf("%s/%s",
		strings.TrimSuffix(imageBuild.Status.ArtifactURL, "/"),
		imageBuild.Status.ArtifactFileName)

	outputPath := filepath.Join(outputDir, imageBuild.Status.ArtifactFileName)

	fmt.Printf("Downloading artifact from: %s\n", artifactURL)
	fmt.Printf("Saving to: %s\n", outputPath)

	// Wait for server to be ready before attempting download
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	// Wait for server to be ready
	fmt.Println("Waiting for artifact server to be ready...")
	err := waitForServer(client, artifactURL)
	if err != nil {
		fmt.Printf("Error waiting for server: %v\n", err)
		return
	}

	maxRetries := 5
	for retry := range maxRetries {
		if retry > 0 {
			backoffTime := time.Duration(retry*2) * time.Second
			fmt.Printf("Waiting %v before retry %d/%d...\n", backoffTime, retry+1, maxRetries)
			time.Sleep(backoffTime)
		}

		if err := downloadFile(artifactURL, outputPath); err != nil {
			if retry < maxRetries-1 {
				fmt.Printf("Attempt %d failed: %v. Will retry.\n", retry+1, err)
				continue
			}
			fmt.Printf("Error downloading file after %d attempts: %v\n", maxRetries, err)
			return
		}

		fmt.Printf("Artifact downloaded successfully to %s\n", outputPath)

		if fileInfo, err := os.Stat(outputPath); err == nil {
			fileSizeMB := float64(fileInfo.Size()) / 1024 / 1024
			fmt.Printf("File size: %.2f MB\n", fileSizeMB)
		}

		return
	}
}

func waitForServer(client *http.Client, url string) error {
	maxAttempts := 30
	interval := time.Second * 2

	for i := range maxAttempts {
		resp, err := client.Head(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
				return nil
			}
		}
		if i < maxAttempts-1 {
			time.Sleep(interval)
		}
	}
	return fmt.Errorf("server not ready after %d attempts", maxAttempts)
}

func downloadFile(url string, filepath string) error {
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	client := &http.Client{
		Timeout: 30 * time.Minute,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // TODO change later
			},
		},
	}

	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	bar := progressbar.DefaultBytes(
		resp.ContentLength,
		"Downloading",
	)

	_, err = io.Copy(io.MultiWriter(out, bar), resp.Body)
	return err
}

func waitForBuildCompletion(c client.Client, name, namespace string, timeoutMinutes int) (*automotivev1.ImageBuild, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()

	var completedBuild *automotivev1.ImageBuild
	var lastPhase, lastMessage string
	var waitingForURL bool

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
				if imageBuild.Spec.ServeArtifact {
					if imageBuild.Status.ArtifactURL != "" {
						if !waitingForURL {
							fmt.Printf("\nstatus: %s - %s\n", imageBuild.Status.Phase, imageBuild.Status.Message)
						}
						return true, nil
					}
					if !waitingForURL {
						fmt.Printf("\nstatus: %s - %s\n", imageBuild.Status.Phase, imageBuild.Status.Message)
						fmt.Print("Waiting for artifact URL to be available...")
						waitingForURL = true
					} else {
						fmt.Print(".")
					}
					return false, nil
				}
				if imageBuild.Status.Phase != lastPhase || imageBuild.Status.Message != lastMessage {
					fmt.Printf("\nstatus: %s - %s\n", imageBuild.Status.Phase, imageBuild.Status.Message)
				}
				return true, nil
			}

			if imageBuild.Status.Phase == "Failed" {
				fmt.Println()
				return false, fmt.Errorf("build failed: %s", imageBuild.Status.Message)
			}

			// Only print full status if it changed
			if imageBuild.Status.Phase != lastPhase || imageBuild.Status.Message != lastMessage {
				fmt.Printf("\nstatus: %s - %s\n", imageBuild.Status.Phase, imageBuild.Status.Message)
				lastPhase = imageBuild.Status.Phase
				lastMessage = imageBuild.Status.Message
			} else {
				fmt.Print(".")
			}

			return false, nil
		})

	fmt.Println() // ensure we end with a newline
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

func runShow(cmd *cobra.Command, args []string) {
	ctx := context.Background()

	c, err := getClient()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	buildName := args[0]

	build := &automotivev1.ImageBuild{}
	if err := c.Get(ctx, types.NamespacedName{Name: buildName, Namespace: namespace}, build); err != nil {
		fmt.Printf("Error getting ImageBuild %s: %v\n", buildName, err)
		os.Exit(1)
	}

	fmt.Printf("Name:        %s\n", build.Name)
	fmt.Printf("Namespace:   %s\n", build.Namespace)
	fmt.Printf("Created:     %s\n", build.CreationTimestamp.Format(time.RFC3339))
	fmt.Printf("Status:      %s\n", build.Status.Phase)
	fmt.Printf("Message:     %s\n", build.Status.Message)

	fmt.Printf("\nBuild Specification:\n")
	fmt.Printf("  Distro:             %s\n", build.Spec.Distro)
	fmt.Printf("  Target:             %s\n", build.Spec.Target)
	fmt.Printf("  Architecture:       %s\n", build.Spec.Architecture)
	fmt.Printf("  Export Format:      %s\n", build.Spec.ExportFormat)
	fmt.Printf("  Mode:               %s\n", build.Spec.Mode)
	fmt.Printf("  Manifest ConfigMap:      %s\n", build.Spec.ManifestConfigMap)
	fmt.Printf("  OSBuild Image:      %s\n", build.Spec.AutomativeOSBuildImage)
	fmt.Printf("  Storage Class:      %s\n", build.Spec.StorageClass)
	fmt.Printf("  Serve Artifact:     %v\n", build.Spec.ServeArtifact)
	fmt.Printf("  Serve Expiry Hours: %d\n", build.Spec.ServeExpiryHours)

	if build.Status.Phase == "Completed" {
		fmt.Printf("\nArtifacts:\n")
		fmt.Printf("  PVC Name:       %s\n", build.Status.PVCName)
		fmt.Printf("  Artifact Path:  %s\n", build.Status.ArtifactPath)
		fmt.Printf("  File Name:      %s\n", build.Status.ArtifactFileName)
		if build.Status.ArtifactURL != "" {
			fmt.Printf("  Download URL:    %s\n", build.Status.ArtifactURL)
		}
	}

}

func getClient() (client.Client, error) {
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
	err = exec.Stream(remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		return fmt.Errorf("exec error: %v, stderr: %s", err, stderr.String())
	}

	return nil
}
