// Package scenarios contains long-running tests that simulate real-world conditions
// to validate the dynamic behavior of the Elara controller.
package scenarios

import (
	"context"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	scalingv1alpha1 "elara/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The createDeployment helper function is assumed to be available in this package.

var _ = Describe("Scenario: Complex Plateau Test", func() {

	const (
		scenarioTimeout    = time.Minute * 5
		tickInterval       = time.Second * 2
		simulationDuration = time.Minute * 1 // A shorter simulation, focused on a single event.
		namespace          = "default"
	)

	It("should correctly scale a complex topology of groups and independent deployments", func(ctx SpecContext) {
		By("Setting up a complex test environment with multiple nodes and groups")

		// --- Create 5 Worker Nodes ---
		var nodes []*corev1.Node
		initialNodePower := 100.0
		for i := 0; i < 5; i++ {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        fmt.Sprintf("scenario-node-complex-%d", i),
					Annotations: map[string]string{"elara.dev/power-consumption": fmt.Sprintf("%.2f", initialNodePower)},
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			nodes = append(nodes, node)
		}
		// Initial total power = 5 * 100 = 500W

		// --- Create Deployments for all entities ---
		// We call createDeployment but don't need to store the returned objects,
		// as we fetch them later by name. This resolves any "declared and not used" errors.
		createDeployment(ctx, k8sClient, "data-processor", namespace, 5)
		createDeployment(ctx, k8sClient, "data-ingestor", namespace, 5)
		createDeployment(ctx, k8sClient, "api-gateway", namespace, 4)
		createDeployment(ctx, k8sClient, "frontend-web", namespace, 4)
		createDeployment(ctx, k8sClient, "cache-service", namespace, 3)
		createDeployment(ctx, k8sClient, "log-aggregator", namespace, 3)
		// Initial total replicas = 5+5+4+4+3+3 = 24

		// --- Create ElaraScaler with a complex spec ---
		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "complex-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.10"),
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 10 * time.Second},
					Decrease:          metav1.Duration{Duration: 20 * time.Second},
					IncreaseTolerance: resource.MustParse("0.10"), // A tight tolerance is fine for a stable plateau.
					DecreaseTolerance: resource.MustParse("0.20"),
				},
				DeploymentGroups: []scalingv1alpha1.DeploymentGroupSpec{
					{
						Name: "analytics-group",
						Members: []scalingv1alpha1.GroupMemberSpec{
							{Weight: 2, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "data-processor", Namespace: namespace, MinReplicas: 5, MaxReplicas: 50}},
							{Weight: 1, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "data-ingestor", Namespace: namespace, MinReplicas: 5, MaxReplicas: 50}},
						},
					},
					{
						Name: "web-services-group",
						Members: []scalingv1alpha1.GroupMemberSpec{
							{Weight: 3, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "api-gateway", Namespace: namespace, MinReplicas: 4, MaxReplicas: 10}},  // Low max
							{Weight: 2, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "frontend-web", Namespace: namespace, MinReplicas: 4, MaxReplicas: 12}}, // Low max
						},
					},
				},
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "cache-service", Namespace: namespace, MinReplicas: 3, MaxReplicas: 20}},
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "log-aggregator", Namespace: namespace, MinReplicas: 3, MaxReplicas: 20}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

		collector := NewCollector()
		deploymentNames := []string{
			"data-processor", "data-ingestor", "api-gateway", "frontend-web",
			"cache-service", "log-aggregator",
		}

		// --- SIMULATION PHASE 1: Initial Stable Period ---
		By("Observing the system in its initial stable state")
		initialDuration := 14 * time.Second // Observe for 14 seconds before the event
		initialTicker := time.NewTicker(tickInterval)
		initialEndTime := time.Now().Add(initialDuration)

		for now := range initialTicker.C {
			if now.After(initialEndTime) {
				initialTicker.Stop()
				break
			}
			// During this phase, power is constant at 500W.
			// This allows the controller to initialize and stabilize.
			collectAllData(ctx, collector, 500.0, scaler, deploymentNames, namespace)
		}

		// --- SIMULATION PHASE 2: The Power Jump Event ---
		// This happens atomically between observation loops. We update all nodes at once
		// to create a clean step-up signal, not a rapid ramp.
		By("--- POWER JUMP EVENT: Setting all nodes to high power plateau ---")
		newNodePower := 200.0
		// Use a WaitGroup to update all nodes in parallel, making the change near-instantaneous.
		var wg sync.WaitGroup
		for _, node := range nodes {
			wg.Add(1)
			// Launch each update in its own goroutine.
			go func(n *corev1.Node) {
				defer wg.Done()
				defer GinkgoRecover() // Important for goroutines in Ginkgo tests

				nodeKey := client.ObjectKeyFromObject(n)
				Eventually(ctx, func() error {
					err := k8sClient.Get(ctx, nodeKey, n)
					if err != nil {
						return err
					}
					n.Annotations["elara.dev/power-consumption"] = fmt.Sprintf("%.2f", newNodePower)
					return k8sClient.Update(ctx, n)
				}, "5s", "100ms").Should(Succeed())
			}(node)
		}
		// Wait for all node updates to complete before continuing the test.
		wg.Wait()

		// --- SIMULATION PHASE 3: Post-Jump Observation ---
		// Now we observe how the controller reacts to the new, stable, high-power state.
		By("Observing the system's reaction to the power plateau")
		observationDuration := simulationDuration - initialDuration
		observationTicker := time.NewTicker(tickInterval)
		defer observationTicker.Stop()
		observationEndTime := time.Now().Add(observationDuration)

		for now := range observationTicker.C {
			if now.After(observationEndTime) {
				break
			}
			// During this phase, power is constant at 1000W.
			collectAllData(ctx, collector, 1000.0, scaler, deploymentNames, namespace)
		}

		By("Exporting collected data to CSV")
		err := collector.ExportToCSV("complex_plateau_results.csv")
		Expect(err).NotTo(HaveOccurred())

		// Final assertions can be added here to programmatically verify the outcome.
		// For example, the total number of replicas should have increased significantly.
		Expect(collector.DataPoints[len(collector.DataPoints)-1].TotalReplicas).To(BeNumerically(">", 24))

		By("Cleaning up resources")
		Expect(k8sClient.Delete(ctx, scaler)).Should(Succeed())
		for _, name := range deploymentNames {
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dep))).Should(Succeed())
		}
		for _, node := range nodes {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
		}
	}, SpecTimeout(scenarioTimeout))
})

// collectAllData is a helper function to avoid duplicating the data collection logic.
func collectAllData(ctx context.Context, collector *Collector, currentTotalPower float64, scaler *scalingv1alpha1.ElaraScaler, depNames []string, ns string) {
	// Fetch the latest state of the ElaraScaler object
	scalerKey := client.ObjectKeyFromObject(scaler)
	fetchedScaler := &scalingv1alpha1.ElaraScaler{}
	Expect(k8sClient.Get(ctx, scalerKey, fetchedScaler)).Should(Succeed())

	// Fetch the latest state of all deployments
	replicas := make(map[string]int)
	for _, name := range depNames {
		dep := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: ns}, dep)).Should(Succeed())
		replicas[name] = int(*dep.Spec.Replicas)
	}

	// Extract the reference power for logging
	refPower := 0.0
	if fetchedScaler.Status.ReferencePower != nil {
		refPower = fetchedScaler.Status.ReferencePower.AsApproximateFloat64()
	}

	// Call the collector's method to record the snapshot
	collector.Collect(currentTotalPower, refPower, fetchedScaler.Status.Mode, replicas)
}
