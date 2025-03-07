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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	pod "github.com/tektoncd/pipeline/pkg/apis/pipeline/pod"
	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
	"k8s.io/utils/ptr"
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
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete

// Reconcile ImageBuild
func (r *ImageBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("imagebuild", req.NamespacedName)

	if time.Now().Minute() == 0 {
		if err := r.checkAndCleanupExpiredPods(ctx); err != nil {
			log.Error(err, "Failed to clean up expired pods")
		}
	}

	imageBuild := &automotivev1.ImageBuild{}
	if err := r.Get(ctx, req.NamespacedName, imageBuild); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	log.Info("fetched ImageBuild", "name", imageBuild.Name)

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

			if err := r.updateArtifactInfo(ctx, imageBuild); err != nil {
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

// updateArtifactInfo updates the status with information about accessing the built artifacts
func (r *ImageBuildReconciler) updateArtifactInfo(ctx context.Context, imageBuild *automotivev1.ImageBuild) error {
	log := r.Log.WithValues("imagebuild", types.NamespacedName{Name: imageBuild.Name, Namespace: imageBuild.Namespace})
	pvcName := fmt.Sprintf("%s-shared-workspace", imageBuild.Name)

	var fileExtension string
	if imageBuild.Spec.ExportFormat == "image" {
		fileExtension = ".raw"
	} else if imageBuild.Spec.ExportFormat == "qcow2" {
		fileExtension = ".qcow2"
	} else {
		fileExtension = fmt.Sprintf(".%s", imageBuild.Spec.ExportFormat)
	}

	fileName := fmt.Sprintf("%s-%s-%s%s",
		imageBuild.Spec.Distro,
		imageBuild.Spec.Target,
		imageBuild.Spec.ExportFormat,
		fileExtension)

	log.Info("Setting artifact info", "pvc", pvcName, "fileName", fileName)

	imageBuild.Status.PVCName = pvcName
	imageBuild.Status.ArtifactPath = "/"
	imageBuild.Status.ArtifactFileName = fileName

	if imageBuild.Spec.ServeArtifact {
		podName := fmt.Sprintf("%s-artifact-server", imageBuild.Name)

		if err := r.createArtifactServingPod(ctx, imageBuild); err != nil {
			log.Error(err, "Failed to create artifact serving pod")
		}

		rsyncCommand := fmt.Sprintf("mkdir -p ./output && oc rsync %s:/artifacts/ ./output/ -n %s -c artifact-server",
			podName, imageBuild.Namespace)

		imageBuild.Status.RsyncCommand = rsyncCommand
	}

	return r.Status().Update(ctx, imageBuild)
}

// createArtifactServingPod creates a pod that mounts the build artifacts PVC
// and serves them
func (r *ImageBuildReconciler) createArtifactServingPod(ctx context.Context, imageBuild *automotivev1.ImageBuild) error {
	log := r.Log.WithValues("imagebuild", types.NamespacedName{Name: imageBuild.Name, Namespace: imageBuild.Namespace})

	expiryHours := int32(24)
	if imageBuild.Spec.ServeExpiryHours > 0 {
		expiryHours = imageBuild.Spec.ServeExpiryHours
	}

	expiryTime := metav1.Now().Add(time.Hour * time.Duration(expiryHours))

	// Create a pod that mounts the PVC
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-artifact-server", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"imagebuild-name":              imageBuild.Name,
				"purpose":                      "serve-artifacts",
			},
			Annotations: map[string]string{
				"automotive.sdv.cloud.redhat.com/expiry-time": expiryTime.Format(time.RFC3339),
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
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "artifact-server",
					Image: "quay.io/instrumentisto/rsync-ssh",
					Command: []string{
						"sleep",
						fmt.Sprintf("%d", expiryHours*3600),
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "artifacts",
							MountPath: "/artifacts",
						},
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("10m"),
							corev1.ResourceMemory: resource.MustParse("32Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("50m"),
							corev1.ResourceMemory: resource.MustParse("64Mi"),
						},
					},
				},
			},
			Volumes: []corev1.Volume{
				{
					Name: "artifacts",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: fmt.Sprintf("%s-shared-workspace", imageBuild.Name),
						},
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	if err := r.Create(ctx, pod); err != nil {
		if errors.IsAlreadyExists(err) {
			log.Info("Artifact serving pod already exists")
			return nil
		}
		return fmt.Errorf("failed to create artifact serving pod: %w", err)
	}

	log.Info("Created artifact serving pod", "expiryHours", expiryHours)
	return nil
}

// checkAndCleanupExpiredPods periodically checks for and deletes expired artifact serving pods
func (r *ImageBuildReconciler) checkAndCleanupExpiredPods(ctx context.Context) error {
	log := r.Log.WithName("cleanup")

	podList := &corev1.PodList{}
	if err := r.List(ctx, podList, client.MatchingLabels{"purpose": "serve-artifacts"}); err != nil {
		return fmt.Errorf("failed to list artifact serving pods: %w", err)
	}

	now := metav1.Now()

	for _, pod := range podList.Items {
		// Check if pod has expiry time annotation
		expiryTimeStr, ok := pod.Annotations["automotive.sdv.cloud.redhat.com/expiry-time"]
		if !ok {
			continue
		}

		expiryTime, err := time.Parse(time.RFC3339, expiryTimeStr)
		if err != nil {
			log.Error(err, "Failed to parse expiry time", "pod", pod.Name, "expiry", expiryTimeStr)
			continue
		}

		if now.Time.After(expiryTime) {
			log.Info("Deleting expired artifact serving pod", "pod", pod.Name, "namespace", pod.Namespace, "expiry", expiryTime)

			if err := r.Delete(ctx, &pod); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete expired artifact serving pod", "pod", pod.Name)
			}
		}
	}

	return nil
}

func (r *ImageBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool { return false },
			DeleteFunc: func(e event.DeleteEvent) bool { return false },
			UpdateFunc: func(e event.UpdateEvent) bool { return false },
			GenericFunc: func(e event.GenericEvent) bool {
				return true
			},
		}).
		Complete(reconcile.Func(func(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
			if err := r.checkAndCleanupExpiredPods(ctx); err != nil {
				r.Log.Error(err, "Failed to clean up expired pods")
			}
			return reconcile.Result{RequeueAfter: time.Hour}, nil
		})); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.ImageBuild{}).
		Owns(&tektonv1.PipelineRun{}).
		Owns(&corev1.Pod{}).
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
