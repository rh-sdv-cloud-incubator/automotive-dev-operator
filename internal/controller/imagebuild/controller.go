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

package imagebuild

import (
	"context"
	//tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=security.openshift.io,resources=securitycontextconstraints,verbs=get;list;watch;create;update;patch;delete;use
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=tasks;pipelines;pipelineruns;taskruns,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ImageBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *ImageBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&automotivev1.ImageBuild{}).
		Complete(r)
}

func (r *ImageBuildReconciler) createPipelineRun() {

	// av :=

	// // Create the PipelineRun
	// pipelineRun := r.generatePipelineRun(
	// 	av.Name,
	// 	av.Namespace,
	// 	[]tektonv1.Param{
	// 		{
	// 			Name: "arch",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.Architecture,
	// 			},
	// 		},
	// 		{
	// 			Name: "distro",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.Distro,
	// 			},
	// 		},
	// 		{
	// 			Name: "target",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.Target,
	// 			},
	// 		},
	// 		{
	// 			Name: "mode",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.Mode,
	// 			},
	// 		},
	// 		{
	// 			Name: "export-format",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.ExportFormat,
	// 			},
	// 		},
	// 		{
	// 			Name: "storage-class",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.StorageClass,
	// 			},
	// 		},
	// 		{
	// 			Name: "automotive-osbuild-image",
	// 			Value: tektonv1.ParamValue{
	// 				Type:      tektonv1.ParamTypeString,
	// 				StringVal: imageBuild.Spec.AutomativeOSBuildImage,
	// 			},
	// 		},
	// 	},
	// 	[]tektonv1.WorkspaceBinding{
	// 		{
	// 			Name: "shared-workspace",
	// 			VolumeClaimTemplate: &corev1.PersistentVolumeClaim{
	// 				Spec: corev1.PersistentVolumeClaimSpec{
	// 					AccessModes: []corev1.PersistentVolumeAccessMode{
	// 						corev1.ReadWriteOnce,
	// 					},
	// 					Resources: corev1.VolumeResourceRequirements{
	// 						Requests: corev1.ResourceList{
	// 							corev1.ResourceStorage: resource.Quantity{},
	// 						},
	// 					},
	// 				},
	// 			},
	// 		},
	// 		{
	// 			Name: "mpp-config-workspace",
	// 			ConfigMap: &corev1.ConfigMapVolumeSource{
	// 				LocalObjectReference: corev1.LocalObjectReference{
	// 					Name: imageBuild.Spec.MppConfigMap,
	// 				},
	// 			},
	// 		},
	// 	},
	// 	nil, // runtimeClassName
	// )
	// if err := r.Create(ctx, pipelineRun); err != nil {
	// 	log.Error(err, "Failed to create PipelineRun")
	// 	return ctrl.Result{}, err
	// }

}
