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

	"github.com/go-logr/logr"
	securityv1 "github.com/openshift/api/security/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"fmt"

	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
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

	// Fetch ImageBuild instance
	log.Info("Fetching ImageBuild")
	imageBuild := &automotivev1.ImageBuild{}
	if err := r.Get(ctx, req.NamespacedName, imageBuild); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Create SCC
	log.Info("Creating SCC for ImageBuild")
	scc := &securityv1.SecurityContextConstraints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("automotive-build-scc-%s", imageBuild.Namespace),
			Namespace: imageBuild.Namespace,
		},
		AllowHostPorts:           false,
		AllowPrivilegedContainer: true,
		RunAsUser: securityv1.RunAsUserStrategyOptions{
			Type: securityv1.RunAsUserStrategyRunAsAny,
		},
		Users: []string{
			fmt.Sprintf("system:serviceaccount:%s:pipeline", imageBuild.Namespace),
		},
		AllowHostDirVolumePlugin: false,
		AllowHostIPC:             false,
		SELinuxContext: securityv1.SELinuxContextStrategyOptions{
			Type: securityv1.SELinuxStrategyRunAsAny,
		},
		// ... other SCC fields as per template
	}

	if err := r.Create(ctx, scc); err != nil && !errors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	// Create RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pipeline-privileged-scc",
			Namespace: imageBuild.Namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "system:openshift:scc:privileged",
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      "pipeline",
				Namespace: imageBuild.Namespace,
			},
		},
	}

	if err := r.Create(ctx, rb); err != nil && !errors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	// Create Pipeline
	pipeline := &tektonv1.Pipeline{
		ObjectMeta: metav1.ObjectMeta{
			Name:      imageBuild.Name,
			Namespace: imageBuild.Namespace,
		},
		Spec: tektonv1.PipelineSpec{
			Params: []tektonv1.ParamSpec{
				{
					Name: "distro",
					Type: "string",
					Default: &tektonv1.ParamValue{
						StringVal: "cs9",
					},
				},
				// ... other params
			},
			Tasks: []tektonv1.PipelineTask{
				{
					Name: "create-build-pvc",
					TaskRef: &tektonv1.TaskRef{
						Name: "create-pvc",
					},
					// ... task config
				},
				// ... other tasks based on publishers
			},
		},
	}

	// Add publisher tasks based on imageBuild.Spec.Publishers
	for _, pub := range imageBuild.Spec.Publishers {
		if pub.Type == "registry" && pub.Registry != nil {
			// Add registry publisher task
			// ...
		}
	}

	if err := r.Create(ctx, pipeline); err != nil && !errors.IsAlreadyExists(err) {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ImageBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.ImageBuild{}).
		Complete(r)
}
