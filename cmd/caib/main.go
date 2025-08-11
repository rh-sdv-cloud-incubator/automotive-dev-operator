package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"

	"k8s.io/apimachinery/pkg/util/wait"

	progressbar "github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automotivev1 "github.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
	buildapitypes "github.com/rh-sdv-cloud-incubator/automotive-dev-operator/internal/buildapi"
	buildapiclient "github.com/rh-sdv-cloud-incubator/automotive-dev-operator/internal/buildapi/client"
)

type progressReporter struct {
	writer           io.Writer
	progressCallback func(n int)
}

func (pr *progressReporter) Write(p []byte) (int, error) {
	n, err := pr.writer.Write(p)
	if pr.progressCallback != nil {
		pr.progressCallback(n)
	}
	return n, err
}

var (
	serverURL              string
	kubeconfig             string
	namespace              string
	imageBuildCfg          string
	manifest               string
	buildName              string
	distro                 string
	target                 string
	architecture           string
	exportFormat           string
	mode                   string
	automotiveImageBuilder string
	storageClass           string
	runtimeClassName       string
	outputDir              string
	timeout                int
	waitForBuild           bool
	download               bool
	exposeRoute            bool
	customDefs             []string
	followLogs             bool
	version                string
	aibExtraArgs           string
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "caib",
		Short:   "Cloud Automotive Image Builder",
		Version: version,
	}

	rootCmd.InitDefaultVersionFlag()
	rootCmd.SetVersionTemplate("caib version: {{.Version}}\n")

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

	buildCmd.Flags().StringVar(&serverURL, "server", os.Getenv("CAIB_SERVER"), "REST API server base URL (e.g. https://api.example)")
	buildCmd.Flags().StringVar(&imageBuildCfg, "config", "", "path to ImageBuild YAML configuration file")
	buildCmd.Flags().StringVar(&manifest, "manifest", "", "path to manifest YAML file for the build")
	buildCmd.Flags().StringVar(&buildName, "name", "", "name for the ImageBuild")
	buildCmd.Flags().StringVar(&distro, "distro", "cs9", "distribution to build")
	buildCmd.Flags().StringVar(&target, "target", "qemu", "target platform")
	buildCmd.Flags().StringVar(&architecture, "arch", "arm64", "architecture (amd64, arm64)")
	buildCmd.Flags().StringVar(&exportFormat, "export-format", "image", "export format (image, qcow2)")
	buildCmd.Flags().StringVar(&mode, "mode", "image", "build mode")
	buildCmd.Flags().StringVar(&automotiveImageBuilder, "automotive-image-builder", "quay.io/centos-sig-automotive/automotive-image-builder:1.0.0", "container image for automotive-image-builder")
	buildCmd.Flags().StringVar(&storageClass, "storage-class", "", "storage class for build PVC")
	buildCmd.Flags().StringVar(&runtimeClassName, "runtime-class", "", "runtime class name for build pods")
	buildCmd.Flags().IntVar(&timeout, "timeout", 60, "timeout in minutes when waiting for build completion")
	buildCmd.Flags().BoolVarP(&waitForBuild, "wait", "w", false, "wait for the build to complete")
	buildCmd.Flags().BoolVarP(&download, "download", "d", false, "automatically download artifacts when build completes")
	buildCmd.Flags().BoolVarP(&exposeRoute, "route", "r", false, "use a route for downloading artifacts")
	buildCmd.Flags().BoolVarP(&followLogs, "follow", "f", false, "follow logs of the build")
	buildCmd.Flags().StringArrayVar(&customDefs, "define", []string{}, "Custom definition in KEY=VALUE format (can be specified multiple times)")
	buildCmd.Flags().StringVar(&aibExtraArgs, "aib-args", "", "extra arguments passed to automotive-image-builder (space-separated)")
	buildCmd.Flags().StringVarP(&namespace, "namespace", "n", "automotive-dev-operator-system", "namespace where the ImageBuild exists")

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

	if err := validateBuildRequirements(); err != nil {
		handleError(err)
	}

	if serverURL == "" {
		handleError(fmt.Errorf("--server is required"))
	}

	if serverURL != "" {
		api, err := buildapiclient.New(serverURL)
		if err != nil {
			handleError(err)
		}

		manifestBytes, err := os.ReadFile(manifest)
		if err != nil {
			handleError(fmt.Errorf("error reading manifest: %w", err))
		}

		// Validate and parse bounded type fields
		parsedDistro, err := buildapitypes.ParseDistro(distro)
		if err != nil {
			handleError(err)
		}
		parsedTarget, err := buildapitypes.ParseTarget(target)
		if err != nil {
			handleError(err)
		}
		parsedArch, err := buildapitypes.ParseArchitecture(architecture)
		if err != nil {
			handleError(err)
		}
		parsedExportFormat, err := buildapitypes.ParseExportFormat(exportFormat)
		if err != nil {
			handleError(err)
		}
		parsedMode, err := buildapitypes.ParseMode(mode)
		if err != nil {
			handleError(err)
		}

		// Parse AIB extra args from space-separated string to array
		var aibArgsArray []string
		if strings.TrimSpace(aibExtraArgs) != "" {
			aibArgsArray = strings.Fields(aibExtraArgs)
		}

		req := buildapitypes.BuildRequest{
			Name:                   buildName,
			Manifest:               string(manifestBytes),
			ManifestFileName:       filepath.Base(manifest),
			Distro:                 parsedDistro,
			Target:                 parsedTarget,
			Architecture:           parsedArch,
			ExportFormat:           parsedExportFormat,
			Mode:                   parsedMode,
			AutomotiveImageBuilder: automotiveImageBuilder,
			StorageClass:           storageClass,
			RuntimeClassName:       runtimeClassName,
			CustomDefs:             customDefs,
			AIBExtraArgs:           aibArgsArray,
			ServeArtifact:          download || exposeRoute,
			ExposeRoute:            exposeRoute,
		}

		resp, err := api.CreateBuild(ctx, req)
		if err != nil {
			handleError(err)
		}
		fmt.Printf("Build %s accepted: %s - %s\n", resp.Name, resp.Phase, resp.Message)
		// If manifest references local files, upload them via the API
		localRefs, err := findLocalFileReferences(string(manifestBytes))
		if err != nil {
			handleError(fmt.Errorf("manifest file reference error: %w", err))
		}
		if len(localRefs) > 0 {
			for _, ref := range localRefs {
				if _, err := os.Stat(ref["source_path"]); err != nil {
					handleError(fmt.Errorf("referenced file %s does not exist: %w", ref["source_path"], err))
				}
			}

			uploads := make([]buildapiclient.Upload, 0, len(localRefs))
			for _, ref := range localRefs {
				uploads = append(uploads, buildapiclient.Upload{SourcePath: ref["source_path"], DestPath: ref["source_path"]})
			}
			if err := api.UploadFiles(ctx, resp.Name, uploads); err != nil {
				handleError(fmt.Errorf("upload files failed: %w", err))
			}
			fmt.Println("Local files uploaded. Build will proceed.")
		}

		if waitForBuild || followLogs || download || exposeRoute {
			fmt.Println("Waiting for build to complete...")
			timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Minute)
			defer cancel()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			userFollowRequested := followLogs
			var lastPhase, lastMessage string
			logFollowWarned := false
			for {
				select {
				case <-timeoutCtx.Done():
					handleError(fmt.Errorf("timed out waiting for build"))
				case <-ticker.C:
					if followLogs {
						req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/v1/builds/"+url.PathEscape(resp.Name)+"/logs?follow=1", nil)
						resp2, err := http.DefaultClient.Do(req)
						if err == nil && resp2.StatusCode == http.StatusOK {
							fmt.Println("Streaming logs...")
							io.Copy(os.Stdout, resp2.Body)
							resp2.Body.Close()
							followLogs = false
						} else if resp2 != nil {
							body, _ := io.ReadAll(resp2.Body)
							msg := strings.TrimSpace(string(body))
							if resp2.StatusCode == http.StatusServiceUnavailable {
								if !logFollowWarned && msg != "" {
									fmt.Printf("log stream unavailable: %s\n", msg)
									logFollowWarned = true
								}
							} else {
								if msg != "" {
									fmt.Printf("log stream error (%d): %s\n", resp2.StatusCode, msg)
								} else {
									fmt.Printf("log stream error: HTTP %d\n", resp2.StatusCode)
								}
								followLogs = false
							}
							resp2.Body.Close()
						}
					}
					reqCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
					st, err := api.GetBuild(reqCtx, resp.Name)
					cancel()
					if err != nil {
						fmt.Printf("status check failed: %v\n", err)
						continue
					}
					if !userFollowRequested {
						if st.Phase != lastPhase || st.Message != lastMessage {
							fmt.Printf("status: %s - %s\n", st.Phase, st.Message)
							lastPhase = st.Phase
							lastMessage = st.Message
						}
					}
					if st.Phase == "Completed" {
						if download && st.ArtifactURL != "" {
							fmt.Printf("Artifact available: %s\n", st.ArtifactURL)
						}
						return
					}
					if st.Phase == "Failed" {
						handleError(fmt.Errorf("build failed: %s", st.Message))
					}
				}
			}
		}
		return
	}

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
		isPathDirCmd := []string{"sh", "-c", fmt.Sprintf("if [ -d '%s' ]; then echo 'directory'; elif [ -f '%s' ]; then echo 'file'; else echo 'notfound'; fi", podPath, podPath)}

		req := clientset.CoreV1().RESTClient().Post().
			Resource("pods").
			Name(podName).
			Namespace(namespace).
			SubResource("exec").
			VersionedParams(&corev1.PodExecOptions{
				Container: containerName,
				Command:   isPathDirCmd,
				Stdout:    true,
				Stderr:    true,
			}, scheme.ParameterCodec)

		exec, err := remotecommand.NewSPDYExecutor(configCopy, "POST", req.URL())
		if err != nil {
			return fmt.Errorf("error creating SPDY executor: %w", err)
		}

		var pathTypeStdout, pathTypeStderr bytes.Buffer
		err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdout: &pathTypeStdout,
			Stderr: &pathTypeStderr,
		})

		if err != nil {
			return fmt.Errorf("error checking path type: %v, stderr: %s", err, pathTypeStderr.String())
		}

		pathType := strings.TrimSpace(pathTypeStdout.String())
		if pathType == "notfound" {
			return fmt.Errorf("path %s does not exist on the pod", podPath)
		}

		fmt.Printf("Path type for %s: %s\n", podPath, pathType)

		if pathType == "directory" {
			fmt.Printf("Downloading directory %s via chunked tar streaming\n", podPath)

			if err := os.MkdirAll(localPath, 0755); err != nil {
				return fmt.Errorf("error creating local directory: %w", err)
			}

			tempTarFile := filepath.Join(os.TempDir(), fmt.Sprintf("download-%d.tar", time.Now().UnixNano()))
			fmt.Printf("Using temporary file: %s\n", tempTarFile)

			tarFile, err := os.Create(tempTarFile)
			if err != nil {
				return fmt.Errorf("error creating temp tar file: %w", err)
			}
			defer func() {
				tarFile.Close()
				os.Remove(tempTarFile)
			}()

			tarCmd := []string{"sh", "-c", fmt.Sprintf("cd %s && tar -cf - .", podPath)}
			req = clientset.CoreV1().RESTClient().Post().
				Resource("pods").
				Name(podName).
				Namespace(namespace).
				SubResource("exec").
				VersionedParams(&corev1.PodExecOptions{
					Container: containerName,
					Command:   tarCmd,
					Stdout:    true,
					Stderr:    true,
				}, scheme.ParameterCodec)

			exec, err = remotecommand.NewSPDYExecutor(configCopy, "POST", req.URL())
			if err != nil {
				return fmt.Errorf("error creating SPDY executor for tar: %w", err)
			}

			fmt.Println("Starting download... (this may take a while for large directories)")

			startTime := time.Now()
			lastUpdateTime := startTime
			var bytesReceived int64

			progressWriter := &progressReporter{
				writer: tarFile,
				progressCallback: func(n int) {
					bytesReceived += int64(n)

					if time.Since(lastUpdateTime) > 1*time.Second {
						elapsed := time.Since(startTime)
						rate := float64(bytesReceived) / (1024 * 1024 * elapsed.Seconds())
						fmt.Printf("Downloaded %.2f MB (%.2f MB/s)\n",
							float64(bytesReceived)/(1024*1024), rate)
						lastUpdateTime = time.Now()
					}
				},
			}

			var stderr bytes.Buffer
			err = exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
				Stdout: progressWriter,
				Stderr: &stderr,
			})

			if err != nil {
				return fmt.Errorf("exec error during tar download: %v, stderr: %s", err, stderr.String())
			}

			elapsed := time.Since(startTime)
			rate := float64(bytesReceived) / (1024 * 1024 * elapsed.Seconds())
			fmt.Printf("Downloaded %.2f MB (%.2f MB/s)\n",
				float64(bytesReceived)/(1024*1024), rate)

			tarFile.Close()

			fmt.Printf("Download complete. Extracting to %s\n", localPath)

			readTarFile, err := os.Open(tempTarFile)
			if err != nil {
				return fmt.Errorf("error opening tar file for extraction: %w", err)
			}
			defer readTarFile.Close()

			tr := tar.NewReader(readTarFile)
			extractedFiles := 0

			for {
				header, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("error reading tar header: %w", err)
				}

				target := filepath.Join(localPath, header.Name)

				switch header.Typeflag {
				case tar.TypeDir:
					if err := os.MkdirAll(target, 0755); err != nil {
						return fmt.Errorf("error creating directory %s: %w", target, err)
					}
				case tar.TypeReg:
					parentDir := filepath.Dir(target)
					if err := os.MkdirAll(parentDir, 0755); err != nil {
						return fmt.Errorf("error creating parent dir for %s: %w", target, err)
					}

					f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
					if err != nil {
						return fmt.Errorf("error creating file %s: %w", target, err)
					}

					if _, err := io.Copy(f, tr); err != nil {
						f.Close()
						return fmt.Errorf("error extracting file %s: %w", target, err)
					}
					f.Close()
					extractedFiles++

					if extractedFiles%1000 == 0 {
						fmt.Printf("Extracted %d files...\n", extractedFiles)
					}
				case tar.TypeSymlink:
					parentDir := filepath.Dir(target)
					if err := os.MkdirAll(parentDir, 0755); err != nil {
						return fmt.Errorf("error creating parent dir for symlink %s: %w", target, err)
					}

					if err := os.Symlink(header.Linkname, target); err != nil {
						return fmt.Errorf("error creating symlink %s -> %s: %w", target, header.Linkname, err)
					}
				}
			}

			fmt.Printf("Directory %s extracted successfully with %d files\n", podPath, extractedFiles)
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
		return
	}

	if fileInfo, err := os.Stat(outputPath); err == nil {
		if fileInfo.IsDir() {
			fmt.Printf("Artifact directory downloaded successfully to %s\n", outputPath)
		} else {
			fileSizeMB := float64(fileInfo.Size()) / 1024 / 1024
			fmt.Printf("Artifact downloaded successfully to %s (%.2f MB)\n", outputPath, fileSizeMB)
		}
	} else {
		fmt.Printf("Artifact downloaded but unable to get file info: %v\n", err)
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
