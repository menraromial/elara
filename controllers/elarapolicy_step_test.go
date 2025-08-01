package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1"
)

const (
	stepTestTimeout  = time.Second * 30
	stepTestInterval = time.Millisecond * 250
)

var _ = Describe("ElaraPolicy Controller: Step Function Test", func() {

	const (
		StepTestNamespace = "elara-step-test"
		StepPolicyName    = "step-test-policy"
		NodeName          = "step-test-node"
	)

	ctx := context.Background()

	BeforeEach(func() {
		By("Creating namespace for step function test")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: StepTestNamespace}}
		Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
	})

	AfterEach(func() {
		By("Cleaning up step function test resources")

		// Step 1: Delete the ElaraPolicy first to stop reconciliation.
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: StepPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())

		// Step 2: Delete the test node to prevent further reconciliation
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())

		// Step 3: Explicitly delete all deployments in the namespace.
		By("Explicitly deleting all mock deployments")
		deploymentList := &appsv1.DeploymentList{}
		Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(StepTestNamespace))).Should(Succeed())
		for _, dep := range deploymentList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &dep))).Should(Succeed())
		}

		// Step 4: Wait for all deployments to be gone.
		By("Waiting for all deployments to be deleted")
		Eventually(func() (int, error) {
			err := k8sClient.List(ctx, deploymentList, client.InNamespace(StepTestNamespace))
			if err != nil {
				return -1, err
			}
			return len(deploymentList.Items), nil
		}, stepTestTimeout, stepTestInterval).Should(BeZero(), "All deployments should be deleted")

		// Step 5: Delete the namespace (best effort cleanup)
		By("Deleting the test namespace")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: StepTestNamespace}}
		_ = client.IgnoreNotFound(k8sClient.Delete(ctx, ns))
		// Note: We don't wait for namespace deletion as it can be slow in test environments
	})

	It("should converge to the correct state quickly after a large power drop", func() {
		// --- SETUP ---
		managedDeployments := []greenopsv1.ManagedDeployment{
			{Name: "frontend", Namespace: StepTestNamespace, MinReplicas: 2, MaxReplicas: 20, Weight: resource.MustParse("0.6"), Group: "web"},
			{Name: "backend", Namespace: StepTestNamespace, MinReplicas: 3, MaxReplicas: 15, Weight: resource.MustParse("0.4"), Group: "web"},
			{Name: "database", Namespace: StepTestNamespace, MinReplicas: 2, MaxReplicas: 5},
			{Name: "critical-api", Namespace: StepTestNamespace, MinReplicas: 4, MaxReplicas: 5},
		}

		By("Creating mock deployments at their optimal state")
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}

		By("Creating a mock node at optimal power")
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: "100", currentPowerLabel: "100"}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		defer k8sClient.Delete(ctx, node)

		By("Creating the ElaraPolicy to trigger initial reconciliation")
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: StepPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring all deployments are at their max replicas initially")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments {
			expectedInitialReplicas[md.Name] = md.MaxReplicas
		}
		assertAllDeploymentsConverged(ctx, StepTestNamespace, expectedInitialReplicas)

		By("Injecting a 60% power drop (100% -> 40%) and starting the timer")

		expectedFinalReplicas := map[string]int32{
			"frontend":     5,
			"backend":      7,
			"database":     2,
			"critical-api": 4,
		}

		startTime := time.Now()

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
		node.Labels[currentPowerLabel] = "40"
		Expect(k8sClient.Update(ctx, node)).Should(Succeed())

		assertAllDeploymentsConverged(ctx, StepTestNamespace, expectedFinalReplicas)

		convergenceTime := time.Since(startTime)

		GinkgoLogr.Info("METRIC: Convergence Time", "duration_ms", convergenceTime.Milliseconds())
		fmt.Printf("\nMETRIC - Convergence Time: %d ms\n", convergenceTime.Milliseconds())

		Expect(convergenceTime).To(BeNumerically("<", 10*time.Second), "System should converge in a reasonable time.")
	})
})
