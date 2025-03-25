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

package failovers

import (
	"context"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
	"github.com/christensenjairus/Failover-Operator/internal/controller"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Mock an override of syncToOtherClusters to avoid nil pointer in tests
func (r *Manager) syncToOtherClustersTest(ctx context.Context, fo *crdv1alpha1.Failover) {
	// Do nothing in test
	if r.MCReconciler == nil {
		return
	}
	// Only call the real method if MCReconciler is not nil
	r.syncToOtherClusters(ctx, fo)
}

var _ = Describe("Failover Manager", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		failover := &crdv1alpha1.Failover{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind Failover")
			err := K8sClient.Get(ctx, typeNamespacedName, failover)
			if err != nil && errors.IsNotFound(err) {
				resource := &crdv1alpha1.Failover{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: crdv1alpha1.FailoverSpec{
						FailoverMode: "UPTIME",
						FailoverGroups: []crdv1alpha1.FailoverGroupReference{
							{
								Name: "test-group",
							},
						},
						TargetCluster: "cluster1",
					},
				}
				Expect(K8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &crdv1alpha1.Failover{}
			err := K8sClient.Get(ctx, typeNamespacedName, resource)
			if err == nil {
				Expect(K8sClient.Delete(ctx, resource)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			// Create a context with SyncMode=true to avoid the nil pointer
			testCtx := context.WithValue(ctx, controller.SyncModeKey, true)

			controllerReconciler := &Manager{
				Client: K8sClient,
				Scheme: K8sClient.Scheme(),
				// MCReconciler is nil for testing
			}

			_, err := controllerReconciler.Reconcile(testCtx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
