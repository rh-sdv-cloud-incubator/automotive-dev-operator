package automotivedev

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	tektonv1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/internal/controller/tekton"

	automotivev1 "gitlab.com/rh-sdv-cloud-incubator/automotive-dev-operator/api/v1"
)

// AutomotiveDevReconciler reconciles a AutomotiveDev object
type AutomotiveDevReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Log    logr.Logger
}

const (
	DefaultNamespace = "automotive-dev"
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

	pipeline := tekton.GenerateTektonPipeline("automotive-build-pipeline", av.Namespace)
	if err := r.createOrUpdatePipeline(ctx, pipeline); err != nil {
		log.Error(err, "Failed to create/update Pipeline")
		return ctrl.Result{}, err
	}

	log.Info("Pipeline created successfully", "name", pipeline.Name)

	tasks := tekton.GenerateTektonTasks(av.Namespace)
	for _, task := range tasks {
		if err := r.createOrUpdateTask(ctx, task); err != nil {
			log.Error(err, "Failed to create/update Task", "task", task.Name)
			return ctrl.Result{}, err
		}

		log.Info("Task created successfully", "name", task.Name)
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
