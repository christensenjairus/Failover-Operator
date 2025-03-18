package controller

import (
	replicationv1alpha1 "github.com/csi-addons/kubernetes-csi-addons/api/replication.storage/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
)

// RegisterSchemes registers the VolumeReplication schema
func RegisterSchemes(scheme *runtime.Scheme) error {
	if err := replicationv1alpha1.AddToScheme(scheme); err != nil {
		return err
	}
	return nil
}
