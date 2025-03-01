package imagebuild

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/utils/ptr"
	pod "github.com/tektoncd/pipeline/pkg/apis/pipeline/pod"
	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
)

const (
	OperatorNamespace = "automotive-dev-operator-system"
)

// ImageBuildReconciler reconciles a ImageBuild object
type ImageBuildReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=imagebuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=imagebuilds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=automotive.sdv.cloud.redhat.com,resources=imagebuilds/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=get;list;watch;create;update;patch;delete;use
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=tasks;pipelines;pipelineruns;taskruns,verbs=get;list;watch;create;update;patch;delete

func (r *ImageBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("imagebuild", req.NamespacedName)

	imageBuild := &automotivev1.ImageBuild{}
	if err := r.Get(ctx, req.NamespacedName, imageBuild); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Check if PipelineRun already exists
	existingPipelineRuns := &tektonv1.PipelineRunList{}
	if err := r.List(ctx, existingPipelineRuns,
		client.InNamespace(req.Namespace),
		client.MatchingLabels{"imagebuild-name": imageBuild.Name}); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Checking existing PipelineRuns")

	if len(existingPipelineRuns.Items) > 0 {
		lastRun := existingPipelineRuns.Items[len(existingPipelineRuns.Items)-1]

		if !isPipelineRunCompleted(lastRun) {
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}

		if isSuccessful(lastRun) {
			if err := r.updateStatus(ctx, imageBuild, "Completed", "Image build completed successfully"); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			if err := r.updateStatus(ctx, imageBuild, "Failed", "Image build failed"); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if err := r.createPipelineRun(ctx, imageBuild); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, imageBuild, "Building", "Image build started"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: time.Second * 30}, nil
}

func (r *ImageBuildReconciler) createPipelineRun(ctx context.Context, imageBuild *automotivev1.ImageBuild) error {
	log := r.Log.WithValues("imagebuild", types.NamespacedName{Name: imageBuild.Name, Namespace: imageBuild.Namespace})
	log.Info("Creating PipelineRun for ImageBuild")

	// First get the pipeline from the operator namespace to verify it exists
	operatorPipeline := &tektonv1.Pipeline{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      "automotive-build-pipeline",
		Namespace: OperatorNamespace,
	}, operatorPipeline); err != nil {
		return fmt.Errorf("failed to get operator pipeline: %w", err)
	}

	nodeAffinity := &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
			NodeSelectorTerms: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Key:      "kubernetes.io/arch",
							Operator: corev1.NodeSelectorOpIn,
							Values:   []string{imageBuild.Spec.Architecture},
						},
					},
				},
			},
		},
	}

	params := []tektonv1.Param{
		{
			Name: "arch",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.Architecture,
			},
		},
		{
			Name: "distro",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.Distro,
			},
		},
		{
			Name: "target",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.Target,
			},
		},
		{
			Name: "mode",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.Mode,
			},
		},
		{
			Name: "export-format",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.ExportFormat,
			},
		},
		{
			Name: "storage-class",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.StorageClass,
			},
		},
		{
			Name: "automotive-osbuild-image",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.AutomativeOSBuildImage,
			},
		},
	}

	workspaces := []tektonv1.WorkspaceBinding{
		{
			Name: "shared-workspace",
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: fmt.Sprintf("%s-shared-workspace", imageBuild.Name),
			},
		},
		{
			Name: "mpp-config-workspace",
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: imageBuild.Spec.MppConfigMap,
				},
			},
		},
	}

	if imageBuild.Spec.Publishers != nil && imageBuild.Spec.Publishers.Registry != nil {
		params = append(params,
			tektonv1.Param{
				Name: "repository-url",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: imageBuild.Spec.Publishers.Registry.RepositoryURL,
				},
			},
			tektonv1.Param{
				Name: "secret-ref",
				Value: tektonv1.ParamValue{
					Type:      tektonv1.ParamTypeString,
					StringVal: imageBuild.Spec.Publishers.Registry.Secret,
				},
			},
		)
	}

	// Create the workspace PVC if it doesn't exist
	storageSize := resource.MustParse("8Gi")
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-shared-workspace", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"imagebuild-name":              imageBuild.Name,
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
			StorageClassName: &imageBuild.Spec.StorageClass,
		},
	}

	if err := r.Create(ctx, pvc); err != nil {
		if !errors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create shared workspace PVC: %w", err)
		}
	}

	// Create a PipelineRun with the resolver reference only
	pipelineRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-run-", imageBuild.Name),
			Namespace:    imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"imagebuild-name":              imageBuild.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: imageBuild.APIVersion,
					Kind:       imageBuild.Kind,
					Name:       imageBuild.Name,
					UID:        imageBuild.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: &tektonv1.PipelineRef{
				// Use only the ResolverRef, not the Name field
				ResolverRef: tektonv1.ResolverRef{
					Resolver: "cluster",
					Params: []tektonv1.Param{
						{
							Name: "kind",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: "pipeline",
							},
						},
						{
							Name: "name",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: "automotive-build-pipeline",
							},
						},
						{
							Name: "namespace",
							Value: tektonv1.ParamValue{
								Type:      tektonv1.ParamTypeString,
								StringVal: OperatorNamespace,
							},
						},
					},
				},
			},
			Params:     params,
			Workspaces: workspaces,
		},
	}

	pipelineRun.Spec.TaskRunSpecs = []tektonv1.PipelineTaskRunSpec{
		{
			PipelineTaskName: "build-image",
			PodTemplate: &pod.PodTemplate{
				Affinity: &corev1.Affinity{
					NodeAffinity: nodeAffinity,
				},
			},
		},
	}

	if err := r.Create(ctx, pipelineRun); err != nil {
		return fmt.Errorf("failed to create PipelineRun: %w", err)
	}

	log.Info("Successfully created PipelineRun", "name", pipelineRun.Name)
	return nil
}

func (r *ImageBuildReconciler) updateStatus(ctx context.Context, imageBuild *automotivev1.ImageBuild, phase, message string) error {
	imageBuild.Status.Phase = phase
	imageBuild.Status.Message = message

	if phase == "Building" {
		now := metav1.Now()
		imageBuild.Status.StartTime = &now
	} else if phase == "Completed" || phase == "Failed" {
		now := metav1.Now()
		imageBuild.Status.CompletionTime = &now
	}

	return r.Status().Update(ctx, imageBuild)
}

func (r *ImageBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.ImageBuild{}).
		Owns(&tektonv1.PipelineRun{}).
		Complete(r)
}

func isPipelineRunCompleted(pipelineRun tektonv1.PipelineRun) bool {
	return pipelineRun.Status.CompletionTime != nil
}

func isSuccessful(pipelineRun tektonv1.PipelineRun) bool {
	conditions := pipelineRun.Status.Conditions
	if len(conditions) == 0 {
		return false
	}

	return conditions[0].Status == corev1.ConditionTrue
}
