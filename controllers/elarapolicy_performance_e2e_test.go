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

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

// *** CONSTANTS MOVED HERE to be accessible by helper functions ***
const (
	perfTimeout  = time.Second * 60 // Increased timeout for many objects
	perfInterval = time.Second * 1
)

var _ = Describe("ElaraPolicy Controller Performance E2E", func() {

	// Constants specific to this test suite
	const (
		PerfTestNamespace = "elara-perf-test"
		PerfPolicyName    = "perf-test-policy"
		NumDeployments    = 100 // The number of deployments to simulate
	)

	// Context is created once for all tests in this block.
	ctx := context.Background()

	BeforeEach(func() {
		By("Creating namespace for performance test")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: PerfTestNamespace}}
		Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
	})

	AfterEach(func() {
		By("Cleaning up performance test resources")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: PerfTestNamespace}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ns))).Should(Succeed())
		
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: PerfPolicyName}}
		_ = k8sClient.Delete(ctx, policy)
	})

	It("Should correctly scale a large number of deployments", func() {
		By(fmt.Sprintf("Setting up %d mock deployments and a policy", NumDeployments))
		
		// --- Setup Step 1: Generate specs and create prerequisite resources (Deployments and Nodes) ---
		managedDeployments := generateManagedDeployments(PerfTestNamespace, NumDeployments)
		for _, managedDep := range managedDeployments {
			dep := createMockDeployment(managedDep.Namespace, managedDep.Name, managedDep.MaxReplicas)
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		// *** FIX: Create the Node BEFORE creating the policy ***
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "perf-node", Labels: map[string]string{optimalPowerLabel: "1000", currentPowerLabel: "1000"}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		defer k8sClient.Delete(ctx, node) // defer ensures it's cleaned up

		// --- Setup Step 2 (Act): Create the ElaraPolicy, which triggers the first reconciliation ---
		// By creating this last, we ensure the controller has a valid environment on its first run.
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: PerfPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		// --- Assert 1: All deployments should eventually be at their max replicas ---
		By("Asserting initial state is at max replicas")
		expectedReplicas := make(map[string]int32)
		for _, md := range managedDeployments {
			expectedReplicas[md.Name] = md.MaxReplicas
		}
		assertAllDeploymentsConverged(ctx, PerfTestNamespace, expectedReplicas)

		// --- Act 2: Simulate a significant power drop ---
		By("Simulating a 30% power drop")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "perf-node"}, node)).Should(Succeed())
		node.Labels[currentPowerLabel] = "700"
		Expect(k8sClient.Update(ctx, node)).Should(Succeed())
		
		// --- Assert 2: All deployments should scale down to their new target ---
		By("Asserting that all deployments have scaled down")
		scaler := &DeclarativeScaler{Deployments: managedDeployments}
		targetStates := scaler.CalculateTargetState(0.30)
		expectedReplicasAfterDrop := make(map[string]int32)
		for _, ts := range targetStates {
			expectedReplicasAfterDrop[ts.Name] = ts.FinalReplicas
		}
		
		assertAllDeploymentsConverged(ctx, PerfTestNamespace, expectedReplicasAfterDrop)
	})
})

// generateManagedDeployments creates a diverse list of deployment specs for the policy.
func generateManagedDeployments(namespace string, count int) []greenopsv1.ManagedDeployment {
	deployments := make([]greenopsv1.ManagedDeployment, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("app-%03d", i)
		group := fmt.Sprintf("group-%d", i%5)
		
		deployments[i] = greenopsv1.ManagedDeployment{
			Name:        name,
			Namespace:   namespace,
			MinReplicas: 1,
			MaxReplicas: int32(5 + (i % 15)),
			Weight:      resource.MustParse("1.0"),
			Group:       group,
		}
	}
	for i := 0; i < count/10; i++ {
		deployments[i].Group = ""
	}
	return deployments
}

// assertAllDeploymentsConverged is an efficient assertion helper.
func assertAllDeploymentsConverged(ctx context.Context, namespace string, expectedReplicas map[string]int32) {
	Eventually(func(g Gomega) {
		deploymentList := &appsv1.DeploymentList{}
		err := k8sClient.List(ctx, deploymentList, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred(), "Should be able to list deployments")
		
		currentReplicas := make(map[string]int32)
		for _, dep := range deploymentList.Items {
			if dep.Spec.Replicas != nil {
				currentReplicas[dep.Name] = *dep.Spec.Replicas
			} else {
				currentReplicas[dep.Name] = 0 
			}
		}

		g.Expect(currentReplicas).To(Equal(expectedReplicas), "All deployments should converge to their target replica counts")

	}, perfTimeout, perfInterval).Should(Succeed())
}