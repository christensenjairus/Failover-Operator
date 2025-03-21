package controller

import (
	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller/failover"
	"github.com/christensenjairus/Failover-Operator/internal/controller/failovergroup"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
)

// RegisterSchemes registers all required API schemas
func RegisterSchemes(scheme *runtime.Scheme) error {
	if err := crdv1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	if err := replicationv1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	return nil
}

// SetupControllers sets up all controllers with the manager
func SetupControllers(mgr ctrl.Manager, clusterName string) error {
	// Set up Failover controller
	if err := (&failover.FailoverReconciler{
		Client: mgr.GetClient(),
		Logger: ctrl.Log.WithName("controllers").WithName("Failover"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	// Set up FailoverGroup controller
	if err := (&failovergroup.FailoverGroupReconciler{
		Client:      mgr.GetClient(),
		Scheme:      mgr.GetScheme(),
		Log:         ctrl.Log.WithName("controllers").WithName("FailoverGroup"),
		ClusterName: clusterName,
	}).SetupWithManager(mgr); err != nil {
		return err
	}

	return nil
}
