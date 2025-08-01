package controllers

import (
	"context"
	"fmt"

	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

// PowerStep defines a single plateau in our power plan.
type PowerStep struct {
	PowerFactor float64 // e.g., 0.8 for 80% power.
	Duration    int     // in number of cycles/steps.
}

var _ = Describe("ElaraPolicy Controller: Complex Plateau Test", func() {

	const (
		PlateauTestNamespace = "default"
		PlateauPolicyName    = "plateau-test-policy"
		StepDelay            = 3 * time.Second // Time to wait between data collection points.
		plateauTimeout       = 120 * time.Second // Generous timeout for setup and heavy cleanup.
		plateauInterval      = 250 * time.Millisecond
	)

	ctx := context.Background()

	// This robust AfterEach block ensures tests do not hang and resources are cleaned up.
	AfterEach(func() {
		By("Cleaning up complex plateau test resources")

		// Delete the policy to stop the controller from acting on cleanup events.
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: PlateauPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())

		// Delete all test nodes.
		nodeList := &corev1.NodeList{}
		Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "plateau"})).Should(Succeed())
		for _, node := range nodeList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &node))).Should(Succeed())
		}
		
		// Efficiently delete all deployments created by this test.
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(PlateauTestNamespace), client.MatchingLabels{"elara-test": "plateau"})).Should(Succeed())
		
		// Wait until all deployments are confirmed deleted before proceeding.
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(PlateauTestNamespace), client.MatchingLabels{"elara-test": "plateau"})
			return len(list.Items)
		}, plateauTimeout, plateauInterval).Should(BeZero(), "All test deployments should be deleted")
	})

	It("should track discrete power plateaus in a complex, heterogeneous environment", func() {
		// --- SETUP ---
		// Use the helper function to generate a complex topology of deployments.
		managedDeployments := generateComplexTopologyForP(PlateauTestNamespace, 30)
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "plateau"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		// Create multiple nodes with different power capacities.
		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "powerful-node-1", Labels: map[string]string{"elara-test": "plateau", optimalPowerLabel: "1000", currentPowerLabel: "1000"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "medium-node-2",   Labels: map[string]string{"elara-test": "plateau", optimalPowerLabel: "500", currentPowerLabel: "500"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "small-node-3",    Labels: map[string]string{"elara-test": "plateau", optimalPowerLabel: "250", currentPowerLabel: "250"}}},
		}
		var optimalPower float64
		for _, node := range nodes {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
			optimalPower += opt
		}
		
		// Create the policy that governs the deployments.
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: PlateauPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, PlateauTestNamespace, expectedInitialReplicas)

		// --- POWER PLAN EXECUTION & DATA COLLECTION ---
		// Define a sequence of power levels and their durations.
		powerPlan := []PowerStep{
			{PowerFactor: 1.0, Duration: 2}, // Start stable
			{PowerFactor: 0.7, Duration: 4}, // Drop to 70% and hold
			{PowerFactor: 0.5, Duration: 4}, // Drop to 50% and hold
			{PowerFactor: 0.9, Duration: 3}, // Recover to 90% and hold
			{PowerFactor: 1.0, Duration: 2}, // Return to optimal
		}

		By("Executing power plateau plan and collecting data")
		var collectedData []RampDataPoint
		scaler := &DeclarativeScaler{Deployments: managedDeployments}
		stepCounter := 0

		for _, planStep := range powerPlan {
			By(fmt.Sprintf("Entering plateau at %.0f%% power for %d cycles", planStep.PowerFactor*100, planStep.Duration))
			currentPower := optimalPower * planStep.PowerFactor

			// Update all nodes proportionally to the new power level.
			nodeList := &corev1.NodeList{}
			Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "plateau"})).Should(Succeed())
			for _, node := range nodeList.Items {
				opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
				node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", opt*planStep.PowerFactor)
				Expect(k8sClient.Update(ctx, &node)).Should(Succeed())
			}

			// Hold at this power level and collect data at each time step.
			for i := 0; i < planStep.Duration; i++ {
				stepCounter++
				time.Sleep(StepDelay)
				dataPoint := collectDataPoint(ctx, scaler, PlateauTestNamespace, currentPower, optimalPower)
				collectedData = append(collectedData, dataPoint)
			}
		}

		// --- FINAL VALIDATION & REPORTING ---
		By("Asserting final state has returned to max replicas")
		assertAllDeploymentsConverged(ctx, PlateauTestNamespace, expectedInitialReplicas)

		By("Reporting and saving collected metrics")
		header := []string{"Step", "OptimalPower", "CurrentPower", "TargetReplicas", "ActualReplicas", "ReplicationError"}
		records := [][]string{}
		for i, data := range collectedData {
			record := []string{strconv.Itoa(i + 1), fmt.Sprintf("%.2f", data.OptimalPower), fmt.Sprintf("%.2f", data.CurrentPower), strconv.Itoa(int(data.TargetReplicas)), strconv.Itoa(int(data.ActualReplicas)), strconv.Itoa(int(data.Error))}
			records = append(records, record)
		}
		Expect(saveDataToCSV("plateau_error_data.csv", append([][]string{header}, records...))).To(Succeed())
		
		// Print a summary to the console for immediate feedback.
		var totalError int64
		for _, data := range collectedData { totalError += int64(data.Error) }
		avgError := float64(totalError) / float64(len(collectedData))
		fmt.Printf("\n--- METRICS: PLATEAU TEST SUMMARY ---\n")
		fmt.Printf("Average Replication Error: %.2f\n", avgError)
		fmt.Printf("-------------------------------------\n")
		Expect(avgError).To(BeNumerically("<", 10), "Average replication error should be low even in a complex scenario")
	})
})

// generateComplexTopology creates a rich and varied list of deployment specs.
func generateComplexTopologyForP(namespace string, count int) []greenopsv1.ManagedDeployment {
	deployments := make([]greenopsv1.ManagedDeployment, count)
	
	// Group 1: Backend Services (critical, higher min replicas)
	backendCount := count / 3
	for i := 0; i < backendCount; i++ {
		name := fmt.Sprintf("backend-svc-%d", i)
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: 3, MaxReplicas: 10,
			Weight: resource.MustParse(fmt.Sprintf("%.1f", 1.0+float64(i))), Group: "backend-services",
		}
	}

	// Group 2: Frontend Services (less critical, wider scaling range)
	frontendCount := count / 3
	for i := backendCount; i < backendCount+frontendCount; i++ {
		name := fmt.Sprintf("frontend-app-%d", i)
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: 1, MaxReplicas: 20,
			Weight: resource.MustParse("1.0"), Group: "frontend-apps",
		}
	}

	// Independent Deployments (e.g., monitoring, batch jobs)
	for i := backendCount + frontendCount; i < count; i++ {
		name := fmt.Sprintf("independent-job-%d", i)
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: 1, MaxReplicas: 5,
		}
	}
	
	return deployments
}