package imagebuild

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	routev1 "github.com/openshift/api/route/v1"
	pod "github.com/tektoncd/pipeline/pkg/apis/pipeline/pod"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=get;list;watch;create;update;patch;delete;use
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=tasks;pipelines;pipelineruns;taskruns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete;deletecollection

// Reconcile ImageBuild
func (r *ImageBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("imagebuild", req.NamespacedName)

	if time.Now().Minute() == 0 {
		if err := r.checkAndCleanupExpiredResources(ctx); err != nil {
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
		client.MatchingLabels{"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name}); err != nil {
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
			Name: "manifest-config-workspace",
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: imageBuild.Spec.ManifestConfigMap,
				},
			},
		},
	}

	if imageBuild.Spec.StorageClass != "" {
		params = append(params, tektonv1.Param{
			Name: "storage-class",
			Value: tektonv1.ParamValue{
				Type:      tektonv1.ParamTypeString,
				StringVal: imageBuild.Spec.StorageClass,
			},
		})
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

	storageSize := resource.MustParse("8Gi")
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-shared-workspace", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                    "automotive-dev-operator",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         imageBuild.APIVersion,
					Kind:               imageBuild.Kind,
					Name:               imageBuild.Name,
					UID:                imageBuild.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
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

	if imageBuild.Spec.StorageClass != "" {
		pvc.Spec.StorageClassName = &imageBuild.Spec.StorageClass
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
				"app.kubernetes.io/managed-by":                    "automotive-dev-operator",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
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
		if err := r.createArtifactServingResources(ctx, imageBuild); err != nil {
			log.Error(err, "Failed to create artifact serving resources")
			return err
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		route := &routev1.Route{}
		err := wait.PollUntilContextTimeout(
			timeoutCtx,
			time.Second,
			30*time.Second,
			false, // immediate = false
			func(ctx context.Context) (bool, error) {
				if err := r.Get(ctx,
					client.ObjectKey{
						Name:      fmt.Sprintf("%s-artifacts", imageBuild.Name),
						Namespace: imageBuild.Namespace,
					}, route); err != nil {
					return false, err
				}
				return route.Status.Ingress != nil && len(route.Status.Ingress) > 0 &&
					route.Status.Ingress[0].Host != "", nil
			})

		if err != nil {
			return fmt.Errorf("failed to get route hostname: %w", err)
		}

		imageBuild.Status.ArtifactURL = fmt.Sprintf("https://%s", route.Status.Ingress[0].Host)
	}

	return r.Status().Update(ctx, imageBuild)
}

// createArtifactServingResources creates a pod that mounts the build artifacts PVC
// and serves them
func (r *ImageBuildReconciler) createArtifactServingResources(ctx context.Context, imageBuild *automotivev1.ImageBuild) error {
	log := r.Log.WithValues("imagebuild", types.NamespacedName{Name: imageBuild.Name, Namespace: imageBuild.Namespace})

	expiryHours := int32(24)
	if imageBuild.Spec.ServeExpiryHours > 0 {
		expiryHours = imageBuild.Spec.ServeExpiryHours
	}

	expiryTime := metav1.Now().Add(time.Hour * time.Duration(expiryHours))

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-artifact-server", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                    "automotive-dev-operator",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
			},
			Annotations: map[string]string{
				"automotive.sdv.cloud.redhat.com/expiry-time": expiryTime.Format(time.RFC3339),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         imageBuild.APIVersion,
					Kind:               imageBuild.Kind,
					Name:               imageBuild.Name,
					UID:                imageBuild.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromInt(8080),
				},
			},
			Selector: map[string]string{
				"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
			},
		},
	}

	if err := r.Create(ctx, svc); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create service: %w", err)
	}

	// Create the deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-artifact-server", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":                    "automotive-dev-operator",
				"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
			},
			Annotations: map[string]string{
				"automotive.sdv.cloud.redhat.com/expiry-time": expiryTime.Format(time.RFC3339),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         imageBuild.APIVersion,
					Kind:               imageBuild.Kind,
					Name:               imageBuild.Name,
					UID:                imageBuild.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"automotive.sdv.cloud.redhat.com/imagebuild-name": imageBuild.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:stable",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "artifacts",
									MountPath: "/usr/share/nginx/html",
								},
								{
									Name:      "nginx-config",
									MountPath: "/etc/nginx/conf.d/",
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
						{
							Name: "nginx-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-nginx-config", imageBuild.Name),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Create nginx config
	nginxConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-nginx-config", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         imageBuild.APIVersion,
					Kind:               imageBuild.Kind,
					Name:               imageBuild.Name,
					UID:                imageBuild.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Data: map[string]string{
			"default.conf": `
server {
    listen 8080;
    server_name localhost;
    location / {
        root /usr/share/nginx/html;
        autoindex on;
        autoindex_exact_size off;
        autoindex_localtime on;
    }
}`,
		},
	}

	if err := r.Create(ctx, nginxConfig); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create nginx config: %w", err)
	}

	if err := r.Create(ctx, deployment); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	// Create the route
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-artifacts", imageBuild.Name),
			Namespace: imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "automotive-dev-operator",
				"imagebuild-name":              imageBuild.Name,
			},
			Annotations: map[string]string{
				"automotive.sdv.cloud.redhat.com/expiry-time": expiryTime.Format(time.RFC3339),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         imageBuild.APIVersion,
					Kind:               imageBuild.Kind,
					Name:               imageBuild.Name,
					UID:                imageBuild.UID,
					Controller:         ptr.To(true),
					BlockOwnerDeletion: ptr.To(true),
				},
			},
		},
		Spec: routev1.RouteSpec{
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: svc.Name,
			},
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromInt(8080),
			},
		},
	}

	if err := r.Create(ctx, route); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create route: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var hostname string
	err := wait.PollUntilContextTimeout(
		timeoutCtx,
		time.Second,
		30*time.Second,
		false,
		func(ctx context.Context) (bool, error) {
			if err := r.Get(ctx, client.ObjectKey{Name: route.Name, Namespace: route.Namespace}, route); err != nil {
				return false, err
			}
			if route.Spec.Host != "" {
				hostname = route.Spec.Host
				return true, nil
			}
			return false, nil
		})

	if err != nil {
		return fmt.Errorf("failed to get route hostname: %w", err)
	}

	imageBuild.Status.ArtifactURL = fmt.Sprintf("https://%s", hostname)
	if err := r.Status().Update(ctx, imageBuild); err != nil {
		return fmt.Errorf("failed to update ImageBuild status: %w", err)
	}

	log.Info("Created artifact serving resources", "route", hostname)
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
			if err := r.checkAndCleanupExpiredResources(ctx); err != nil {
				r.Log.Error(err, "Failed to clean up expired resources")
			}
			return reconcile.Result{RequeueAfter: time.Hour}, nil
		})); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.ImageBuild{}).
		Owns(&tektonv1.PipelineRun{}).
		Owns(&corev1.Pod{}).
		Owns(&routev1.Route{}).
		Complete(r)
}

func (r *ImageBuildReconciler) checkAndCleanupExpiredResources(ctx context.Context) error {
	log := r.Log.WithName("cleanup")

	routeList := &routev1.RouteList{}
	if err := r.List(ctx, routeList, client.MatchingLabels{
		"app.kubernetes.io/managed-by": "automotive-dev-operator",
	}); err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	now := metav1.Now()

	for _, route := range routeList.Items {
		// Check if route has expiry time annotation
		expiryTimeStr, ok := route.Annotations["automotive.sdv.cloud.redhat.com/expiry-time"]
		if !ok {
			continue
		}

		expiryTime, err := time.Parse(time.RFC3339, expiryTimeStr)
		if err != nil {
			log.Error(err, "Failed to parse expiry time", "route", route.Name)
			continue
		}

		if now.Time.After(expiryTime) {
			log.Info("Found expired resources",
				"route", route.Name,
				"namespace", route.Namespace,
				"expiry", expiryTime)

			svcName := route.Spec.To.Name
			deploymentName := svcName
			configMapName := fmt.Sprintf("%s-nginx-config", deploymentName)

			if err := r.Delete(ctx, &route); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete expired route", "route", route.Name)
			}

			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      svcName,
					Namespace: route.Namespace,
				},
			}
			if err := r.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete service", "service", svcName)
			}

			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      deploymentName,
					Namespace: route.Namespace,
				},
			}
			if err := r.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete deployment", "deployment", deploymentName)
			}

			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: route.Namespace,
				},
			}
			if err := r.Delete(ctx, cm); err != nil && !errors.IsNotFound(err) {
				log.Error(err, "Failed to delete config map", "configmap", configMapName)
			}

			log.Info("Successfully cleaned up expired resources",
				"route", route.Name,
				"service", svcName,
				"deployment", deploymentName,
				"configmap", configMapName)
		}
	}

	return nil
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
