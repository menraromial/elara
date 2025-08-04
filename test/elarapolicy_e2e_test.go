package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

// Constants for the node labels used to determine power.
const (
	optimalPowerLabel   = "rapl/optimal"
	currentPowerLabel   = "rapl/current"
	masterNodeRoleLabel = "node-role.kubernetes.io/master"
)

var _ = Describe("ElaraPolicy Controller E2E", func() {

	const (
		TestNamespace   = "elara-e2e-test"
		PolicyName      = "e2e-test-policy"
		Node1Name       = "worker-node-1"
		Node2Name       = "worker-node-2"
		timeout         = time.Second * 10
		interval        = time.Millisecond * 250
	)

	// Context is created once for all tests in this block.
	ctx := context.Background()

	BeforeEach(func() {
		// Create a namespace for this test
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: TestNamespace}}
		Expect(k8sClient.Create(ctx, ns)).Should(Succeed())

		// Create mock nodes
		node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: Node1Name, Labels: map[string]string{optimalPowerLabel: "100", currentPowerLabel: "100"}}}
		node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: Node2Name, Labels: map[string]string{optimalPowerLabel: "100", currentPowerLabel: "100"}}}
		Expect(k8sClient.Create(ctx, node1)).Should(Succeed())
		Expect(k8sClient.Create(ctx, node2)).Should(Succeed())
	})

	AfterEach(func() {
		// Clean up resources after each test
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: PolicyName}}
		_ = k8sClient.Delete(ctx, policy) // Ignore error if not found

		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: TestNamespace}}
		Expect(k8sClient.Delete(ctx, ns)).Should(Succeed())
		
		node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: Node1Name}}
		_ = k8sClient.Delete(ctx, node1)
		node2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: Node2Name}}
		_ = k8sClient.Delete(ctx, node2)
	})

	Context("When a power drop occurs", func() {
		It("Should scale down deployments according to the policy", func() {
			
			// --- Define the Policy first, as it contains the maxReplicas we need ---
			policy := &greenopsv1.ElaraPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: PolicyName},
				Spec: greenopsv1.ElaraPolicySpec{
					Deployments: []greenopsv1.ManagedDeployment{
						{Name: "frontend", Namespace: TestNamespace, MinReplicas: 1, MaxReplicas: 10, Weight: resource.MustParse("0.6"), Group: "web"},
						{Name: "backend", Namespace: TestNamespace, MinReplicas: 2, MaxReplicas: 8, Weight: resource.MustParse("0.4"), Group: "web"},
						{Name: "database", Namespace: TestNamespace, MinReplicas: 1, MaxReplicas: 3},
					},
				},
			}
			
			// --- Setup: Create mock deployments with their initial replicas set to maxReplicas ---
			for _, managedDep := range policy.Spec.Deployments {
				dep := createMockDeployment(managedDep.Namespace, managedDep.Name, managedDep.MaxReplicas)
				Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
			}

			// --- Act 1: Create the ElaraPolicy, which triggers the first reconciliation ---
			Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

			// --- Assert 1: Initially, all replicas should remain at max ---
			// The assertion is now more robust. We check against the `MaxReplicas` from our policy definition.
			By("Asserting initial state is at max replicas")
			for _, managedDep := range policy.Spec.Deployments {
				Eventually(func() int32 {
					var fetchedDep appsv1.Deployment
					key := types.NamespacedName{Name: managedDep.Name, Namespace: managedDep.Namespace}
					err := k8sClient.Get(ctx, key, &fetchedDep)
					if err != nil { return -1 } // Return an invalid value on error
					return *fetchedDep.Spec.Replicas
				}, timeout, interval).Should(Equal(managedDep.MaxReplicas))
			}

			// --- Act 2: Simulate a 50% power drop ---
			By("Simulating a 50% power drop")
			node1 := &corev1.Node{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: Node1Name}, node1)).Should(Succeed())
			node1.Labels[currentPowerLabel] = "50"
			Expect(k8sClient.Update(ctx, node1)).Should(Succeed())

			node2 := &corev1.Node{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: Node2Name}, node2)).Should(Succeed())
			node2.Labels[currentPowerLabel] = "50"
			Expect(k8sClient.Update(ctx, node2)).Should(Succeed())
			
			// --- Assert 2: Check if replicas are scaled down correctly ---
			// Calculation remains the same: Frontend -> 5, Backend -> 4, Database -> 1
			By("Asserting that deployments have been scaled down")
			Eventually(func() int32 {
				var dep appsv1.Deployment
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "frontend", Namespace: TestNamespace}, &dep)
				return *dep.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(5)))

			Eventually(func() int32 {
				var dep appsv1.Deployment
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "backend", Namespace: TestNamespace}, &dep)
				return *dep.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(4)))

			Eventually(func() int32 {
				var dep appsv1.Deployment
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "database", Namespace: TestNamespace}, &dep)
				return *dep.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(1)))

			// --- Act 3: Restore power ---
			By("Restoring power to optimal")
			// It's good practice to re-fetch the object before updating it.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: Node1Name}, node1)).Should(Succeed())
			node1.Labels[currentPowerLabel] = "100"
			Expect(k8sClient.Update(ctx, node1)).Should(Succeed())
			
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: Node2Name}, node2)).Should(Succeed())
			node2.Labels[currentPowerLabel] = "100"
			Expect(k8sClient.Update(ctx, node2)).Should(Succeed())

			// --- Assert 3: Check if replicas are scaled back up to max ---
			By("Asserting that deployments have scaled back up to their max replicas")
			Eventually(func() int32 {
				var dep appsv1.Deployment
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "frontend", Namespace: TestNamespace}, &dep)
				return *dep.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(10)))

			Eventually(func() int32 {
				var dep appsv1.Deployment
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "backend", Namespace: TestNamespace}, &dep)
				return *dep.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(8)))

			Eventually(func() int32 {
				var dep appsv1.Deployment
				_ = k8sClient.Get(ctx, types.NamespacedName{Name: "database", Namespace: TestNamespace}, &dep)
				return *dep.Spec.Replicas
			}, timeout, interval).Should(Equal(int32(3)))
		})
	})
})

// createMockDeployment is a helper function to create a basic Deployment object for tests.
// *** THIS FUNCTION IS NOW CORRECTED ***
func createMockDeployment(namespace, name string, replicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			// We now correctly set the Spec.Replicas field.
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "main",
						Image: "nginx",
					}},
				},
			},
		},
	}
}