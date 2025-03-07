package automotivedev

import (
	"context"
	"fmt"

	_ "embed"
	"time"

	"github.com/go-logr/logr"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
)

// AutomotiveDevReconciler reconciles a AutomotiveDev object
type AutomotiveDevReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

const (
	TektonResourcesNamespace = "automotive-dev-operator-system"
)

// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=automotivedevs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=automotivedevs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=automotivedevs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=get;list;watch;create;update;patch;delete;use
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=tasks;pipelines;pipelineruns,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *AutomotiveDevReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("automotivedev", req.NamespacedName)
	log.Info("Reconciling AutomotiveDev")

	av := &automotivev1.AutomotiveDev{}
	if err := r.Get(ctx, req.NamespacedName, av); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("AutomotiveDev fetched successfully", "name", av.Name)

	// Create Tasks in the designated namespace FIRST
	tasks := generateTektonTasks(TektonResourcesNamespace)
	for _, task := range tasks {
		if err := r.createOrUpdateTask(ctx, task); err != nil {
			log.Error(err, "Failed to create/update Task", "task", task.Name)
			return ctrl.Result{}, err
		}

		log.Info("Task created successfully", "name", task.Name)
	}

	pipeline := generateTektonPipeline("automotive-build-pipeline", TektonResourcesNamespace)
	if err := r.createOrUpdatePipeline(ctx, pipeline); err != nil {
		log.Error(err, "Failed to create/update Pipeline")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled ")
	return ctrl.Result{}, nil
}

func (r *AutomotiveDevReconciler) createOrUpdatePipeline(ctx context.Context, pipeline *tektonv1.Pipeline) error {
	existingPipeline := &tektonv1.Pipeline{}
	err := r.Get(ctx, client.ObjectKey{Name: pipeline.Name, Namespace: pipeline.Namespace}, existingPipeline)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get Pipeline: %w", err)
		}
		return r.Create(ctx, pipeline)
	}

	pipeline.ResourceVersion = existingPipeline.ResourceVersion
	return r.Update(ctx, pipeline)
}

func (r *AutomotiveDevReconciler) createOrUpdateTask(ctx context.Context, task *tektonv1.Task) error {
	existingTask := &tektonv1.Task{}
	err := r.Get(ctx, client.ObjectKey{Name: task.Name, Namespace: task.Namespace}, existingTask)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get Task: %w", err)
		}
		return r.Create(ctx, task)
	}

	task.ResourceVersion = existingTask.ResourceVersion
	return r.Update(ctx, task)
}

func (r *AutomotiveDevReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.AutomotiveDev{}).
		Complete(r)
}

//go:embed scripts/find_manifest.sh
var findManifestScript string

//go:embed scripts/build_image.sh
var buildImageScript string

//go:embed scripts/push_artifact.sh
var pushArtifactScript string

func generateTektonPipeline(name, namespace string) *tektonv1.Pipeline {
	pipeline := &tektonv1.Pipeline{
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
					Description: "Storage class for the PVC to build on (optional, uses cluster default if not specified)",
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "",
					},
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
					Name:        "repository-url",
					Type:        tektonv1.ParamTypeString,
					Description: "URL of the artifact registry to push to",
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "",
					},
				},
				{
					Name:        "secret-ref",
					Type:        tektonv1.ParamTypeString,
					Description: "Secret reference for registry credentials",
					Default: &tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: "",
					},
				},
			},
			Workspaces: []tektonv1.PipelineWorkspaceDeclaration{
				{Name: "shared-workspace"},
				{Name: "manifest-config-workspace"},
			},
			Tasks: []tektonv1.PipelineTask{
				{
					Name: "build-image",
					TaskRef: &tektonv1.TaskRef{
						ResolverRef: tektonv1.ResolverRef{
							Resolver: "cluster",
							Params: []tektonv1.Param{
								{
									Name: "kind",
									Value: tektonv1.ParamValue{
										Type:      tektonv1.ParamTypeString,
										StringVal: "task",
									},
								},
								{
									Name: "name",
									Value: tektonv1.ParamValue{
										Type:      tektonv1.ParamTypeString,
										StringVal: "build-automotive-image",
									},
								},
								{
									Name: "namespace",
									Value: tektonv1.ParamValue{
										Type:      tektonv1.ParamTypeString,
										StringVal: namespace,
									},
								},
							},
						},
					},
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
							Name: "automotive-osbuild-image",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: "$(params.automotive-osbuild-image)",
							},
						},
					},
					Workspaces: []tektonv1.WorkspacePipelineTaskBinding{
						{Name: "shared-workspace", Workspace: "shared-workspace"},
						{Name: "manifest-config-workspace", Workspace: "manifest-config-workspace"},
					},
					Timeout: &metav1.Duration{Duration: 1 * time.Hour},
				},
				{
					Name: "push-registry",
					TaskRef: &tektonv1.TaskRef{
						ResolverRef: tektonv1.ResolverRef{
							Resolver: "cluster",
							Params: []tektonv1.Param{
								{
									Name: "kind",
									Value: tektonv1.ParamValue{
										Type:      tektonv1.ParamTypeString,
										StringVal: "task",
									},
								},
								{
									Name: "name",
									Value: tektonv1.ParamValue{
										Type:      tektonv1.ParamTypeString,
										StringVal: "push-artifact-registry",
									},
								},
								{
									Name: "namespace",
									Value: tektonv1.ParamValue{
										Type:      tektonv1.ParamTypeString,
										StringVal: namespace,
									},
								},
							},
						},
					},
					Params: []tektonv1.Param{
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
							Name: "export-format",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: "$(params.export-format)",
							},
						},
						{
							Name: "repository-url",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: "$(params.repository-url)",
							},
						},
						{
							Name: "secret-ref",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: "$(params.secret-ref)",
							},
						},
					},
					Workspaces: []tektonv1.WorkspacePipelineTaskBinding{
						{Name: "shared-workspace", Workspace: "shared-workspace"},
					},
					RunAfter: []string{"build-image"},
					When: []tektonv1.WhenExpression{
						{
							Input:    "$(params.repository-url)",
							Operator: "notin",
							Values:   []string{"", "null"},
						},
						{
							Input:    "$(params.secret-ref)",
							Operator: "notin",
							Values:   []string{"", "null"},
						},
					},
				},
			},
		},
	}

	return pipeline
}

func generateTektonTasks(namespace string) []*tektonv1.Task {
	tasks := []*tektonv1.Task{
		generateBuildAutomotiveImageTask(namespace),
		generatePushArtifactRegistryTask(namespace),
	}
	return tasks
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
				{
					Name:        "distro",
					Type:        tektonv1.ParamTypeString,
					Description: "Distribution to build",
				},
				{
					Name:        "target",
					Type:        tektonv1.ParamTypeString,
					Description: "Build target",
				},
				{
					Name:        "export-format",
					Type:        tektonv1.ParamTypeString,
					Description: "Export format for the build",
				},
				{
					Name:        "repository-url",
					Type:        tektonv1.ParamTypeString,
					Description: "URL of the artifact registry",
				},
				{
					Name:        "secret-ref",
					Type:        tektonv1.ParamTypeString,
					Description: "Name of the secret containing registry credentials",
				},
			},
			Workspaces: []tektonv1.WorkspaceDeclaration{
				{
					Name:        "shared-workspace",
					Description: "Workspace containing the build artifacts",
					MountPath:   "/workspace/shared",
				},
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
					Script:     pushArtifactScript,
					WorkingDir: "/workspace/shared",
					VolumeMounts: []corev1.VolumeMount{
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
				{
					Name:        "target-architecture",
					Type:        tektonv1.ParamTypeString,
					Description: "Target architecture for the build",
				},
				{
					Name:        "distro",
					Type:        tektonv1.ParamTypeString,
					Description: "Distribution to build",
				},
				{
					Name:        "target",
					Type:        tektonv1.ParamTypeString,
					Description: "Build target",
				},
				{
					Name:        "mode",
					Type:        tektonv1.ParamTypeString,
					Description: "Build mode",
				},
				{
					Name:        "export-format",
					Type:        tektonv1.ParamTypeString,
					Description: "Export format for the build",
				},
				{
					Name:        "automotive-osbuild-image",
					Type:        tektonv1.ParamTypeString,
					Description: "Automotive OSBuild container image to use",
				},
			},
			Results: []tektonv1.TaskResult{
				{
					Name:        "manifest-file-path",
					Description: "Path to the manifest file used for building",
				},
			},
			Workspaces: []tektonv1.WorkspaceDeclaration{
				{
					Name:        "shared-workspace",
					Description: "Workspace for sharing data between steps",
					MountPath:   "/workspace/shared",
				},
				{
					Name:        "manifest-config-workspace",
					Description: "Workspace for manifest configuration",
					MountPath:   "/workspace/manifest-config",
				},
			},
			Steps: []tektonv1.Step{
				{
					Name:   "find-manifest-file",
					Image:  "quay.io/prometheus/busybox:latest",
					Script: findManifestScript,
				},
				{
					Name:  "build-image",
					Image: "$(params.automotive-osbuild-image)",
					SecurityContext: &corev1.SecurityContext{
						Privileged: ptr.To(true),
						SELinuxOptions: &corev1.SELinuxOptions{
							Type: "unconfined_t",
						},
						Capabilities: &corev1.Capabilities{
							Add: []corev1.Capability{},
						},
					},
					Script: buildImageScript,
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "build-dir",
							MountPath: "/_build",
						},
						{
							Name:      "output-dir",
							MountPath: "/output",
						},
						{
							Name:      "run-dir",
							MountPath: "/run/osbuild",
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "build-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "output-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "run-dir",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			},
		},
	}
}
