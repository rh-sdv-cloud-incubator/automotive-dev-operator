package controller

import (
	"time"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// generateTektonPipeline creates the main Pipeline resource
func generateTektonPipeline(name, namespace string) *tektonv1.Pipeline {
	return &tektonv1.Pipeline{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "tekton.dev/v1",
			Kind:       "Pipeline",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
			},
		},
		Spec: tektonv1.PipelineSpec{
			Params: []tektonv1.ParamSpec{
				{
					Name: "distro",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "cs9",
					},
					Description: "Build for this distro specification",
				},
				{
					Name: "target",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "qemu",
					},
					Description: "Build for this target",
				},
				{
					Name: "arch",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "aarch64",
					},
					Description: "Build for this architecture",
				},
				{
					Name: "export-format",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "image",
					},
					Description: "Export format for the image (qcow2, image)",
				},
				{
					Name: "mode",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "image",
					},
					Description: "Build this image mode (package, image)",
				},
				{
					Name:        "storage-class",
					Type:        tektonv1.ParamTypeString,
					Description: "Storage class for the PVC to build on",
				},
				{
					Name: "automotive-osbuild-image",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "quay.io/centos-sig-automotive/automotive-osbuild:latest",
					},
					Description: "Automotive OSBuild image to use for building",
				},
				{
					Name: "output-pvc-size",
					Type: tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "12Gi",
					},
					Description: "Size of the PVC to create for the build",
				},
			},
			Workspaces: []tektonv1.PipelineWorkspaceDeclaration{
				{Name: "shared-workspace"},
				{Name: "mpp-config-workspace"},
			},
			Tasks: []tektonv1.PipelineTask{
				generateCreatePVCPipelineTask(),
				generateBuildImagePipelineTask(),
			},
		},
	}
}

func generateCreatePVCPipelineTask() tektonv1.PipelineTask {
	return tektonv1.PipelineTask{
		Name: "create-build-pvc",
		TaskRef: &tektonv1.TaskRef{
			Name: "create-pvc",
			Kind: "Task",
		},
		Params: []tektonv1.Param{
			{
				Name: "pvc-name",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "build-pvc-$(context.pipelineRun.uid)",
				},
			},
			{
				Name: "pvc-size",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.output-pvc-size)",
				},
			},
			{
				Name: "storage-class",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.storage-class)",
				},
			},
		},
	}
}

func generateBuildImagePipelineTask() tektonv1.PipelineTask {
	return tektonv1.PipelineTask{
		Name: "build-image",
		TaskRef: &tektonv1.TaskRef{
			Name: "build-automotive-image",
			Kind: tektonv1.NamespacedTaskKind,
		},
		RunAfter: []string{"create-build-pvc"},
		Params: []tektonv1.Param{
			{
				Name: "target-architecture",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.arch)",
				},
			},
			{
				Name: "distro",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.distro)",
				},
			},
			{
				Name: "target",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.target)",
				},
			},
			{
				Name: "mode",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.mode)",
				},
			},
			{
				Name: "export-format",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.export-format)",
				},
			},
			{
				Name: "build-pvc-name",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(tasks.create-build-pvc.results.pvc-name)",
				},
			},
			{
				Name: "automotive-osbuild-image",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: "$(params.automotive-osbuild-image)",
				},
			},
		},
		Workspaces: []tektonv1.WorkspacePipelineTaskBinding{
			{Name: "shared-workspace", Workspace: "shared-workspace"},
			{Name: "mpp-config-workspace", Workspace: "mpp-config-workspace"},
		},
		OnError: tektonv1.PipelineTaskContinue, // Fixed: using the correct type
		Timeout: &metav1.Duration{Duration: 1 * time.Hour},
	}
}

// generateTektonTasks generates all required Tekton Tasks
func generateTektonTasks(namespace string) []*tektonv1.Task {
	return []*tektonv1.Task{
		generateCreatePVCTask(namespace),
		generateBuildAutomotiveImageTask(namespace),
		generatePushArtifactRegistryTask(namespace),
	}
}

func generateCreatePVCTask(namespace string) *tektonv1.Task {
	return &tektonv1.Task{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "tekton.dev/v1",
			Kind:       "Task",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "create-pvc",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"app.kubernetes.io/part-of":    "automotive-dev",
			},
		},
		Spec: tektonv1.TaskSpec{
			Params: []tektonv1.ParamSpec{
				{Name: "pvc-name", Type: tektonv1.ParamTypeString},
				{Name: "pvc-size", Type: tektonv1.ParamTypeString},
				{
					Name:    "storage-class",
					Type:    tektonv1.ParamTypeString,
					Default: &tektonv1.ParamValue{Type: tektonv1.ParamTypeString, StringVal: ""},
				},
			},
			Results: []tektonv1.TaskResult{
				{
					Name:        "pvc-name",
					Description: "The name of the created pvc",
				},
			},
			Steps: []tektonv1.Step{
				{
					Name:  "create-pvc",
					Image: "bitnami/kubectl:latest",
					Script: `#!/usr/bin/env bash
set -e
cat <<EOF | kubectl create -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: $(params.pvc-name)
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: $(params.storage-class)
  resources:
    requests:
      storage: $(params.pvc-size)
EOF
echo -n "$(params.pvc-name)" > $(results.pvc-name.path)`,
				},
			},
		},
	}
}

// Common build image script template
const buildImageScript = `#!/bin/bash
set -e

# Environment preparation
osbuildPath="/usr/bin/osbuild"
storePath="/_build"
runTmp="/run/osbuild/"

# Create necessary directories
mkdir -p "$storePath"
mkdir -p "$runTmp"

if mountpoint -q "$osbuildPath"; then
  exit 0
fi

rootType="system_u:object_r:root_t:s0"
chcon "$rootType" "$storePath"

installType="system_u:object_r:install_exec_t:s0"
if ! mountpoint -q "$runTmp"; then
  mount -t tmpfs tmpfs "$runTmp"
fi

destPath="$runTmp/osbuild"
cp -p "$osbuildPath" "$destPath"
chcon "$installType" "$destPath"

mount --bind "$destPath" "$osbuildPath"

cd $(workspaces.shared-workspace.path)

# Determine file extension based on export format
if [ "$(params.export-format)" = "image" ]; then
  file_extension=".raw"
elif [ "$(params.export-format)" = "qcow2" ]; then
  file_extension=".qcow2"
else
  file_extension="$(params.export-format)"
fi

# Create the export file name with the correct extension
exportFile=$(params.distro)-$(params.target)-$(params.export-format)${file_extension}

mode_param=""
if [ -n "$(params.mode)" ]; then
  mode_param="--mode $(params.mode)"
fi

MPP_FILE=$(cat /tekton/results/mpp-file-path)

CUSTOM_DEFS=""
CUSTOM_DEFS_FILE="$(workspaces.mpp-config-workspace.path)/custom-definitions.env"
if [ -f "$CUSTOM_DEFS_FILE" ]; then
  echo "Processing custom definitions from $CUSTOM_DEFS_FILE"
  while read -r line || [[ -n "$line" ]]; do
    for def in $line; do
      CUSTOM_DEFS+=" --define $def"
    done
  done < "$CUSTOM_DEFS_FILE"
else
  echo "No custom-definitions.env file found"
fi

build_command="automotive-image-builder --verbose \
  build \
  $CUSTOM_DEFS \
  --distro $(params.distro) \
  --target $(params.target) \
  --arch=$(params.target-architecture) \
  --build-dir=/output/_build \
  --export $(params.export-format) \
  --osbuild-manifest=/output/image.json \
  $mode_param \
  $MPP_FILE \
  /output/${exportFile}"

echo "Running the build command: $build_command"
$build_command

pushd /output
ln -s ./${exportFile} ./disk.img
echo "Build command completed. Listing output directory:"
ls -l`

func generateBuildAutomotiveImageTask(namespace string) *tektonv1.Task {
	return &tektonv1.Task{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "tekton.dev/v1",
			Kind:       "Task",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "build-automotive-image",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"app.kubernetes.io/part-of":    "automotive-dev",
			},
		},
		Spec: tektonv1.TaskSpec{
			Params: []tektonv1.ParamSpec{
				{Name: "target-architecture", Type: tektonv1.ParamTypeString},
				{Name: "distro", Type: tektonv1.ParamTypeString},
				{Name: "target", Type: tektonv1.ParamTypeString},
				{Name: "mode", Type: tektonv1.ParamTypeString},
				{Name: "export-format", Type: tektonv1.ParamTypeString},
				{Name: "build-pvc-name", Type: tektonv1.ParamTypeString},
				{Name: "automotive-osbuild-image", Type: tektonv1.ParamTypeString},
			},
			Results: []tektonv1.TaskResult{
				{
					Name:        "mpp-file-path",
					Description: "Path to the MPP file used for building",
				},
			},
			Workspaces: []tektonv1.WorkspaceDeclaration{
				{
					Name:      "shared-workspace",
					MountPath: "/workspace/shared",
				},
				{
					Name:      "mpp-config-workspace",
					MountPath: "/workspace/mpp-config",
				},
			},
			Steps: []tektonv1.Step{
				{
					Name:  "find-mpp-file",
					Image: "busybox",
					Script: `#!/bin/sh
set -e
MPP_FILE=$(find $(workspaces.mpp-config-workspace.path) -name '*.mpp.yml' -type f)
if [ -z "$MPP_FILE" ]; then
  echo "No .mpp.yml file found in the ConfigMap"
  exit 1
fi
echo $MPP_FILE > /tekton/results/mpp-file-path`,
				},
				{
					Name:  "build-image",
					Image: "$(params.automotive-osbuild-image)",
					Env: []corev1.EnvVar{
						{
							Name:  "REGISTRY_AUTH_FILE",
							Value: "/tekton/home/.docker/config.json",
						},
					},
					SecurityContext: &corev1.SecurityContext{
						Privileged: ptr.To(true),
						SELinuxOptions: &corev1.SELinuxOptions{
							Type: "unconfined_t",
						},
					},
					Script: buildImageScript,
					VolumeMounts: []corev1.VolumeMount{
						{Name: "dev", MountPath: "/dev"},
						{Name: "build", MountPath: "/_build"},
						{Name: "task-pvc", MountPath: "/output"},
						{Name: "task-pvc", MountPath: "/run/osbuild"},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "dev",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/dev",
						},
					},
				},
				{
					Name: "build",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "task-pvc",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "$(params.build-pvc-name)",
						},
					},
				},
			},
		},
	}
}

func generatePushArtifactRegistryTask(namespace string) *tektonv1.Task {
	return &tektonv1.Task{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "tekton.dev/v1",
			Kind:       "Task",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "push-artifact-registry",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"app.kubernetes.io/part-of":    "automotive-dev",
			},
		},
		Spec: tektonv1.TaskSpec{
			Params: []tektonv1.ParamSpec{
				{Name: "build-pvc-name", Type: tektonv1.ParamTypeString},
				{Name: "distro", Type: tektonv1.ParamTypeString},
				{Name: "target", Type: tektonv1.ParamTypeString},
				{Name: "export-format", Type: tektonv1.ParamTypeString},
				{Name: "repository-url", Type: tektonv1.ParamTypeString},
				{Name: "secret-ref", Type: tektonv1.ParamTypeString},
			},
			Steps: []tektonv1.Step{
				{
					Name:  "push-artifact",
					Image: "ghcr.io/oras-project/oras:v1.2.0",
					Env: []corev1.EnvVar{
						{
							Name:  "DOCKER_CONFIG",
							Value: "/tekton/home/.docker",
						},
					},
					Script: `#!/bin/sh
set -ex

# Determine file extension based on export format
if [ "$(params.export-format)" = "image" ]; then
  file_extension=".raw"
elif [ "$(params.export-format)" = "qcow2"; then
  file_extension=".qcow2"
else
  file_extension="$(params.export-format)"
fi

# Create the export file name with the correct extension
exportFile=$(params.distro)-$(params.target)-$(params.export-format)${file_extension}

echo "Pushing image to $(params.repository-url)"
oras push --disable-path-validation \
  $(params.repository-url) \
  $exportFile:application/vnd.oci.image.layer.v1.tar

echo "Image pushed successfully to registry"`,
					WorkingDir: "/output",
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "task-pvc",
							MountPath: "/output",
						},
						{
							Name:      "docker-config",
							MountPath: "/tekton/home/.docker/config.json",
							SubPath:   ".dockerconfigjson",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "task-pvc",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "$(params.build-pvc-name)",
						},
					},
				},
				{
					Name: "docker-config",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName: "$(params.secret-ref)",
						},
					},
				},
			},
		},
	}
}
