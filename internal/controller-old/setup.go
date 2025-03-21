package controller

import (
	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
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
