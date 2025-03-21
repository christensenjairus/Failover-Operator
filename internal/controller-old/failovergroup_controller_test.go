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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	crdv1alpha1 "github.com/christensenjairus/Failover-Operator/api/v1alpha1"
)

var _ = Describe("FailoverGroup Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-failovergroup"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}
		failoverGroup := &crdv1alpha1.FailoverGroup{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind FailoverGroup")
			err := k8sClient.Get(ctx, typeNamespacedName, failoverGroup)
			if err != nil && errors.IsNotFound(err) {
				resource := &crdv1alpha1.FailoverGroup{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: crdv1alpha1.FailoverGroupSpec{
						DefaultFailoverMode: "safe",
						Components: []crdv1alpha1.ComponentSpec{
							{
								Name:         "test-component",
								FailoverMode: "fast",
								Workloads: []crdv1alpha1.ResourceRef{
									{
										Kind: "Deployment",
										Name: "test-deployment",
									},
								},
								VolumeReplications: []string{
									"test-volume-replication",
								},
								VirtualServices: []string{
									"test-virtual-service",
								},
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			resource := &crdv1alpha1.FailoverGroup{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance FailoverGroup")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &FailoverGroupReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify the resource has been updated with status fields
			updatedResource := &crdv1alpha1.FailoverGroup{}
			err = k8sClient.Get(ctx, typeNamespacedName, updatedResource)
			Expect(err).NotTo(HaveOccurred())

			// Verify that the FailoverGroup is initialized with PRIMARY state
			Expect(updatedResource.Status.State).To(Equal(string(crdv1alpha1.FailoverGroupStatePrimary)))

			// Initially status will likely show health = OK
			Expect(updatedResource.Status.Health).To(Or(Equal("OK"), Equal("DEGRADED"), Equal("ERROR")))

			// Check that component statuses were initialized
			Expect(updatedResource.Status.Components).To(HaveLen(1))
			Expect(updatedResource.Status.Components[0].Name).To(Equal("test-component"))
		})
	})
})
