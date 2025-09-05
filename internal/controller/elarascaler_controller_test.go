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
	"time"
	//"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	//"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	//"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scalingv1alpha1 "elara/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1" // We will need this later
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ = Describe("ElaraScaler Controller", func() {

	const (
		scalerName = "test-scaler"
		timeout    = time.Second * 10
		interval   = time.Millisecond * 250
	)

	Context("When performing scaling calculations", func() {
		var workerNode *corev1.Node
		var dep1, dep2 *appsv1.Deployment
		var scaler *scalingv1alpha1.ElaraScaler
		var scalerLookupKey types.NamespacedName

		// Helper function to create a deployment
		createDeployment := func(name string, replicas int32) *appsv1.Deployment {
			return &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: &replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": name},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
						Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "nginx"}}},
					},
				},
			}
		}

		BeforeEach(func() {
			// Create a single worker node
			workerNode = &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "worker-node-1",
					Annotations: map[string]string{"elara.dev/power-consumption": "100.0"},
				},
			}
			Expect(k8sClient.Create(ctx, workerNode)).Should(Succeed())

			// Create two deployments
			dep1 = createDeployment("dep1", 5) // Start with 5 replicas
			dep2 = createDeployment("dep2", 5) // Start with 5 replicas
			Expect(k8sClient.Create(ctx, dep1)).Should(Succeed())
			Expect(k8sClient.Create(ctx, dep2)).Should(Succeed())

			// Create the ElaraScaler resource
			scalerLookupKey = types.NamespacedName{Name: "scaling-test-scaler"}
			scaler = &scalingv1alpha1.ElaraScaler{
				ObjectMeta: metav1.ObjectMeta{Name: scalerLookupKey.Name},
				Spec: scalingv1alpha1.ElaraScalerSpec{
					Deadband: resource.MustParse("0.05"), // 5%
					StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
						Increase:          metav1.Duration{Duration: 100 * time.Millisecond}, // Short window for tests
						Decrease:          metav1.Duration{Duration: 100 * time.Millisecond},
						IncreaseTolerance: resource.MustParse("0.01"),
						DecreaseTolerance: resource.MustParse("0.01"),
					},
					IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
						{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "dep1", Namespace: "default", MinReplicas: 2, MaxReplicas: 10}},
						{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "dep2", Namespace: "default", MinReplicas: 2, MaxReplicas: 10}},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

			// Wait for the scaler to initialize to Stable
			Eventually(func() string {
				fetched := &scalingv1alpha1.ElaraScaler{}
				_ = k8sClient.Get(ctx, scalerLookupKey, fetched)
				return fetched.Status.Mode
			}, timeout, interval).Should(Equal("Stable"))
		})

		AfterEach(func() {
			// Clean up all resources
			Expect(k8sClient.Delete(ctx, scaler)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, dep1)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, dep2)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, workerNode)).Should(Succeed())
		})

		It("Should scale UP deployments based on remaining capacity", func() {
			By("Setting dep1 to have less capacity than dep2")
			// dep1: 8/10 replicas -> capacity 2
			// dep2: 5/10 replicas -> capacity 5
			// Total capacity = 7
			// Total replicas = 13
			dep1Key := types.NamespacedName{Name: "dep1", Namespace: "default"}
			fetchedDep1 := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, dep1Key, fetchedDep1)).Should(Succeed())
			*fetchedDep1.Spec.Replicas = 8
			Expect(k8sClient.Update(ctx, fetchedDep1)).Should(Succeed())

			By("Increasing power by ~30% to trigger a scale up of 4 replicas")
			// Total replicas = 13. Power change ~30%. deltaTotal = round(13 * 0.3) = round(3.9) = 4
			workerNode.Annotations["elara.dev/power-consumption"] = "130.0"
			Expect(k8sClient.Update(ctx, workerNode)).Should(Succeed())

			// Expected distribution (by capacity):
			// dep1 gets 4 * (2/7) = 1.14 -> LRM gives 1
			// dep2 gets 4 * (5/7) = 2.85 -> LRM gives 3
			// Expected final state: dep1 -> 8+1=9, dep2 -> 5+3=8

			By("Verifying dep1 scaled to 9 replicas")
			Eventually(func() int32 {
				d := &appsv1.Deployment{}
				_ = k8sClient.Get(ctx, dep1Key, d)
				return *d.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(9)))

			By("Verifying dep2 scaled to 8 replicas")
			Eventually(func() int32 {
				d := &appsv1.Deployment{}
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "dep2", Namespace: "default"}, d)
				return *d.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(8)))
		})

		It("Should scale DOWN deployments based on current consumption", func() {
			By("Setting dep1 to have more replicas than dep2")
			// dep1: 8/10 replicas
			// dep2: 4/10 replicas
			// Total consumption = 12
			dep1Key := types.NamespacedName{Name: "dep1", Namespace: "default"}
			fetchedDep1 := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, dep1Key, fetchedDep1)).Should(Succeed())
			*fetchedDep1.Spec.Replicas = 8
			Expect(k8sClient.Update(ctx, fetchedDep1)).Should(Succeed())

			dep2Key := types.NamespacedName{Name: "dep2", Namespace: "default"}
			fetchedDep2 := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, dep2Key, fetchedDep2)).Should(Succeed())
			*fetchedDep2.Spec.Replicas = 4
			Expect(k8sClient.Update(ctx, fetchedDep2)).Should(Succeed())

			By("Decreasing power by 25% to trigger a scale down of 3 replicas")
			// Total replicas = 12. Power change -25%. deltaTotal = round(12 * -0.25) = -3
			workerNode.Annotations["elara.dev/power-consumption"] = "75.0"
			Expect(k8sClient.Update(ctx, workerNode)).Should(Succeed())

			// Expected final state: dep1 -> 8-2=6, dep2 -> 4-1=3

			// Use Eventually to wait for the final state, ignoring intermediate states.
			By("Verifying dep1 scaled down to 6 replicas")
			Eventually(func() int32 {
				d := &appsv1.Deployment{}
				_ = k8sClient.Get(ctx, dep1Key, d)
				return *d.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(6)))

			By("Verifying dep2 scaled down to 3 replicas")
			Eventually(func() int32 {
				d := &appsv1.Deployment{}
				_ = k8sClient.Get(ctx, dep2Key, d)
				return *d.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(3)))

			// AND crucially, verify that the controller returns to a stable state
			By("Verifying the controller is Stable with the new reference power")
			Eventually(func() string {
				s := &scalingv1alpha1.ElaraScaler{}
				_ = k8sClient.Get(ctx, scalerLookupKey, s)
				// Check that P_ref has been updated, which signals the end of the cycle
				if s.Status.ReferencePower != nil && s.Status.ReferencePower.AsApproximateFloat64() == 75.0 {
					return s.Status.Mode
				}
				return ""
			}, timeout, interval).Should(Equal("Stable"))
		})
	})
	// ... end of Describe block
})
