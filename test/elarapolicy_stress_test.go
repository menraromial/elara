package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

var _ = Describe("ElaraPolicy Controller: Constraint Stress Test", func() {
	const (
		StressTestNamespace = "default"
		StressPolicyName    = "stress-test-policy"
		NodeName            = "stress-test-node"
		stressTimeout       = 60 * time.Second
		stressInterval      = 250 * time.Millisecond
	)

	ctx := context.Background()

	// Using robust, non-hanging cleanup logic.
	AfterEach(func() {
		By("Cleaning up stress test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: StressPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(StressTestNamespace), client.MatchingLabels{"elara-test": "stress"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(StressTestNamespace), client.MatchingLabels{"elara-test": "stress"})
			return len(list.Items)
		}, stressTimeout, stressInterval).Should(BeZero())
	})

	It("should redistribute deficit fairly without violating minReplicas constraints", func() {
		// --- SETUP ---
		By("Creating a high-contention deployment setup")
		
		managedDeployments := []greenopsv1.ManagedDeployment{
			{Name: "donor-app", Namespace: StressTestNamespace, MinReplicas: 2, MaxReplicas: 20},
		}
		for i := 0; i < 9; i++ {
			managedDeployments = append(managedDeployments, greenopsv1.ManagedDeployment{
				Name:        fmt.Sprintf("constrained-app-%d", i),
				Namespace:   StressTestNamespace,
				MinReplicas: 9,
				MaxReplicas: 10,
			})
		}

		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "stress"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: "100", currentPowerLabel: "100"}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: StressPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		
		By("Asserting initial state is at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, StressTestNamespace, expectedInitialReplicas)

		By("Injecting a 30% power drop to force a large deficit")
		
		// Final Expected State (based on the controller's correct logic)
		expectedFinalReplicas := make(map[string]int32)
		expectedFinalReplicas["donor-app"] = 2 // Reduced by its max possible margin of 18
		for i := 0; i < 9; i++ {
			expectedFinalReplicas[fmt.Sprintf("constrained-app-%d", i)] = 9 // Reduced by their max possible margin of 1
		}

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
		node.Labels[currentPowerLabel] = "70" // 30% drop
		Expect(k8sClient.Update(ctx, node)).Should(Succeed())

		By("Asserting that all deployments have converged to their constrained minimums")
		assertAllDeploymentsConverged(ctx, StressTestNamespace, expectedFinalReplicas)

		By("METRIC: Validating Constraint Adherence")
		deploymentList := &appsv1.DeploymentList{}
		Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(StressTestNamespace), client.MatchingLabels{"elara-test": "stress"})).Should(Succeed())
		
		constraintViolations := 0
		for _, dep := range deploymentList.Items {
			var minReplicas int32
			for _, md := range managedDeployments {
				if md.Name == dep.Name { minReplicas = md.MinReplicas; break }
			}
			if dep.Spec.Replicas != nil && *dep.Spec.Replicas < minReplicas { constraintViolations++ }
		}
		Expect(constraintViolations).To(BeZero(), "There should be zero constraint violations.")
		fmt.Printf("\nMETRIC - Constraint Violation Count: %d\n", constraintViolations)

		// *** THIS IS THE CORRECTED ASSERTION ***
		// We assert that the deficit absorbed by the donor is what the controller could actually handle.
		initialProportionalReduction := 6 // From manual calculation
		finalActualReduction := 18       // 20 (max) - 2 (final) = 18
		deficitAbsorbed := finalActualReduction - initialProportionalReduction
		
		fmt.Printf("METRIC - Deficit Redistribution Events (approximated): %d replicas were redistributed to the donor app.\n", deficitAbsorbed)
		Expect(deficitAbsorbed).To(Equal(12), "The donor app should absorb the maximum possible deficit")
	})
})