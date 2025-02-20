/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	securityv1 "github.com/openshift/api/security/v1"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
)

const (
	DefaultNamespace = "automotive-dev"
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
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=get;list;watch;create;update;patch;delete;use
//+kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints/finalizers,verbs=update
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tekton.dev,resources=tasks;pipelines;pipelineruns,verbs=get;list;watch;create;update;patch;delete

func (r *ImageBuildReconciler) ensureNamespace(ctx context.Context, namespaceName string) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceName,
			Labels: map[string]string{
				"automotive.sdv.cloud.redhat.com/managed-by": "automotive-dev-operator",
			},
		},
	}

	err := r.Get(ctx, client.ObjectKey{Name: namespaceName}, &corev1.Namespace{})
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Create(ctx, namespace); err != nil {
				return fmt.Errorf("failed to create namespace: %w", err)
			}
			r.Log.Info("Created namespace", "namespace", namespaceName)
			return nil
		}
		return fmt.Errorf("failed to get namespace: %w", err)
	}
	return nil
}

// getTargetNamespace returns the namespace where the ImageBuild should be processed
func (r *ImageBuildReconciler) getTargetNamespace(imageBuild *automotivev1.ImageBuild) string {
	if imageBuild.Namespace != "" {
		return imageBuild.Namespace
	}
	return DefaultNamespace
}

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ImageBuild object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/reconcile
func (r *ImageBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("imagebuild", req.NamespacedName)

	// Create the ClusterRole for SCC first
	if err := r.createSCCPrivilegedRole(ctx); err != nil {
		return ctrl.Result{}, err
	}

	// Create the ClusterRoleBinding for all service accounts
	if err := r.createSCCPrivilegedClusterRoleBinding(ctx); err != nil {
		return ctrl.Result{}, err
	}

	// Fetch ImageBuild instance
	log.Info("Fetching ImageBuild")
	imageBuild := &automotivev1.ImageBuild{}
	if err := r.Get(ctx, req.NamespacedName, imageBuild); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Get target namespace
	namespace := r.getTargetNamespace(imageBuild)

	// Ensure target namespace exists
	if err := r.ensureNamespace(ctx, namespace); err != nil {
		return ctrl.Result{}, err
	}

	// Verify ConfigMap exists
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, client.ObjectKey{Name: imageBuild.Spec.MppConfigMap, Namespace: namespace}, configMap); err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "ConfigMap not found", "name", imageBuild.Spec.MppConfigMap)
			// Requeue to try again later
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	// Create pipeline ServiceAccount
	if err := r.createPipelineServiceAccount(ctx, namespace); err != nil {
		return ctrl.Result{}, err
	}

	// Create SCC
	log.Info("Creating SCC for ImageBuild")
	if err := r.createSecurityContextConstraints(ctx, namespace); err != nil {
		return ctrl.Result{}, err
	}

	// Create Tekton Tasks
	log.Info("Creating Tekton Tasks")
	tasks := generateTektonTasks(namespace)
	for _, task := range tasks {
		if err := r.UpdateOrCreateTask(ctx, task); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create/update Task: %w", err)
		}
	}

	// Create Tekton Pipeline
	log.Info("Creating Tekton Pipeline")
	pipeline := generateTektonPipeline("automotive-build-pipeline", imageBuild.Namespace)
	if err := r.UpdateOrCreatePipeline(ctx, pipeline); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create/update Pipeline: %w", err)
	}

	// Create PipelineRun
	pipelineRun := &tektonv1.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-run-", imageBuild.Name),
			Namespace:    imageBuild.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/instance": imageBuild.Name,
			},
		},
		Spec: tektonv1.PipelineRunSpec{
			PipelineRef: &tektonv1.PipelineRef{
				Name: "automotive-build-pipeline",
			},
			Params: []tektonv1.Param{
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
					Name: "arch",
					Value: tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: imageBuild.Spec.Architecture,
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
					Name: "mode",
					Value: tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: imageBuild.Spec.Mode,
					},
				},
				{
					Name: "storage-class",
					Value: tektonv1.ParamValue{
						Type:      tektonv1.ParamTypeString,
						StringVal: imageBuild.Spec.StorageClass,
					},
				},
			},
			Workspaces: []tektonv1.WorkspaceBinding{
				{
					Name: "shared-workspace",
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: fmt.Sprintf("%s-workspace", imageBuild.Name),
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
			},
		},
	}

	log.Info("Creating PipelineRun")
	if err := r.Create(ctx, pipelineRun); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create PipelineRun: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.ImageBuild{}).
		Complete(r)
}

func (r *ImageBuildReconciler) createSecurityContextConstraints(ctx context.Context, namespace string) error {
	scc := &securityv1.SecurityContextConstraints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("automotive-build-scc-%s", namespace),
			Namespace: namespace,
		},
		AllowHostPorts:           false,
		AllowPrivilegedContainer: true,
		RunAsUser: securityv1.RunAsUserStrategyOptions{
			Type: securityv1.RunAsUserStrategyRunAsAny,
		},
		Users: []string{
			fmt.Sprintf("system:serviceaccount:%s:pipeline", namespace),
		},
		AllowHostDirVolumePlugin: false,
		AllowHostIPC:             false,
		AllowHostPID:             false,
		AllowHostNetwork:         false,
		AllowPrivilegeEscalation: ptr.To(true),
		ReadOnlyRootFilesystem:   false,
		SELinuxContext: securityv1.SELinuxContextStrategyOptions{
			Type: securityv1.SELinuxStrategyRunAsAny,
		},
		FSGroup: securityv1.FSGroupStrategyOptions{
			Type: securityv1.FSGroupStrategyRunAsAny,
		},
		SupplementalGroups: securityv1.SupplementalGroupsStrategyOptions{
			Type: securityv1.SupplementalGroupsStrategyRunAsAny,
		},
		Volumes: []securityv1.FSType{
			"configMap",
			"downwardAPI",
			"emptyDir",
			"persistentVolumeClaim",
			"projected",
			"secret",
			"nfs",
			"csi",
			// Add other volume types as needed
		},
	}

	if err := r.Create(ctx, scc); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create SecurityContextConstraints: %w", err)
	}

	return nil
}

func (r *ImageBuildReconciler) createPipelineServiceAccount(ctx context.Context, namespace string) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline",
			Namespace: namespace,
		},
	}

	if err := r.Create(ctx, sa); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create pipeline ServiceAccount: %w", err)
	}

	return nil
}

func (r *ImageBuildReconciler) createSCCPrivilegedRole(ctx context.Context) error {
	role := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "scc-privileged-role",
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups:     []string{"security.openshift.io"},
				Resources:     []string{"securitycontextconstraints"},
				ResourceNames: []string{"privileged"},
				Verbs:         []string{"use"},
			},
		},
	}

	if err := r.Create(ctx, role); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ClusterRole: %w", err)
	}
	return nil
}

func (r *ImageBuildReconciler) createSCCPrivilegedClusterRoleBinding(ctx context.Context) error {
	binding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pipeline-scc-privileged",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "scc-privileged-role",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:     "Group",
				APIGroup: "rbac.authorization.k8s.io",
				Name:     "system:serviceaccounts",
			},
		},
	}

	if err := r.Create(ctx, binding); err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create ClusterRoleBinding: %w", err)
	}
	return nil
}

// UpdateOrCreateTask updates an existing task or creates it if it doesn't exist
func (r *ImageBuildReconciler) UpdateOrCreateTask(ctx context.Context, task *tektonv1.Task) error {
	existingTask := &tektonv1.Task{}
	err := r.Get(ctx, client.ObjectKey{Name: task.Name, Namespace: task.Namespace}, existingTask)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get Task: %w", err)
		}
		// Task doesn't exist, create it
		return r.Create(ctx, task)
	}

	// Use Server Side Apply for updates
	task.ResourceVersion = "" // Clear resourceVersion for SSA
	if err := r.Patch(ctx, task, client.Apply, client.ForceOwnership, client.FieldOwner("automotive-dev-operator")); err != nil {
		return fmt.Errorf("failed to patch Task: %w", err)
	}
	return nil
}

// UpdateOrCreatePipeline updates an existing pipeline or creates it if it doesn't exist
func (r *ImageBuildReconciler) UpdateOrCreatePipeline(ctx context.Context, pipeline *tektonv1.Pipeline) error {
	existingPipeline := &tektonv1.Pipeline{}
	err := r.Get(ctx, client.ObjectKey{Name: pipeline.Name, Namespace: pipeline.Namespace}, existingPipeline)
	if err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("failed to get Pipeline: %w", err)
		}
		// Pipeline doesn't exist, create it
		return r.Create(ctx, pipeline)
	}

	// Use Server Side Apply for updates
	pipeline.ResourceVersion = "" // Clear resourceVersion for SSA
	if err := r.Patch(ctx, pipeline, client.Apply, client.ForceOwnership, client.FieldOwner("automotive-dev-operator")); err != nil {
		return fmt.Errorf("failed to patch Pipeline: %w", err)
	}
	return nil
}
