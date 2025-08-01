package controllers

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

var _ = Describe("ElaraPolicy Controller: Solar Simulation Test", func() {

	const (
		SolarTestNamespace = "default"
		SolarPolicyName    = "solar-test-policy"
		NumSolarSteps      = 48
		StepDelay          = 2 * time.Second
		solarTimeout       = 240 * time.Second
		solarInterval      = 250 * time.Millisecond
	)

	ctx := context.Background()

	// Robust cleanup logic
	AfterEach(func() {
		By("Cleaning up solar simulation test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: SolarPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		nodeList := &corev1.NodeList{}
		_ = k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "solar"})
		for _, node := range nodeList.Items {
			_ = k8sClient.Delete(ctx, &node)
		}
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(SolarTestNamespace), client.MatchingLabels{"elara-test": "solar"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(SolarTestNamespace), client.MatchingLabels{"elara-test": "solar"})
			return len(list.Items)
		}, solarTimeout, solarInterval).Should(BeZero())
	})

	It("should track a simulated solar power curve with fluctuations", func() {
		// --- SETUP ---
		By("Creating a complex, heterogeneous cluster topology")
		managedDeployments := generateComplexTopology(SolarTestNamespace, 30)
		for _, md := range managedDeployments {
			// Start deployments at their MINIMUM replicas, simulating night time.
			dep := createMockDeployment(md.Namespace, md.Name, md.MinReplicas) 
			dep.Labels = map[string]string{"elara-test": "solar"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}

		// Nodes start with zero current power, simulating night time.
		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "solar-node-1", Labels: map[string]string{"elara-test": "solar", optimalPowerLabel: "1000", currentPowerLabel: "0"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "solar-node-2", Labels: map[string]string{"elara-test": "solar", optimalPowerLabel: "500", currentPowerLabel: "0"}}},
		}
		var optimalPower float64
		for _, node := range nodes {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
			optimalPower += opt
		}

		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: SolarPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring initial state is at minimum (night time)")
		scaler := &DeclarativeScaler{Deployments: managedDeployments}
		initialTargetStates := scaler.CalculateTargetState(1.0) // 100% reduction
		expectedInitialReplicas := make(map[string]int32)
		for _, ts := range initialTargetStates {
			expectedInitialReplicas[ts.Name] = ts.FinalReplicas
		}
		assertAllDeploymentsConverged(ctx, SolarTestNamespace, expectedInitialReplicas)

		// --- SOLAR DAY SIMULATION & DATA COLLECTION ---
		By("Executing a simulated solar day power curve")
		var collectedData []RampDataPoint
		rand.Seed(time.Now().UnixNano())

		for i := 0; i <= NumSolarSteps; i++ {
			angle := (2.0 * math.Pi * float64(i)) / float64(NumSolarSteps)
			baseFactor := (math.Sin(angle-math.Pi/2) + 1) / 2
			jitter := (rand.Float64() - 0.5) * 0.2
			powerFactor := baseFactor + jitter
			if powerFactor < 0 { powerFactor = 0 }
			if powerFactor > 1 { powerFactor = 1 }
			
			// This helper function is now corrected and robust.
			updatePowerAndCollectSolarData(ctx, powerFactor, optimalPower, &collectedData, scaler, SolarTestNamespace)
		}

		// --- FINAL VALIDATION & REPORTING ---
		By("Asserting final state has returned to minimums (end of day)")
		assertAllDeploymentsConverged(ctx, SolarTestNamespace, expectedInitialReplicas)

		By("Reporting and saving collected metrics for the solar cycle")
		header := []string{"Step", "OptimalPower", "CurrentPower", "TargetReplicas", "ActualReplicas", "ReplicationError"}
		records := [][]string{}
		for i, data := range collectedData {
			record := []string{strconv.Itoa(i + 1), fmt.Sprintf("%.2f", data.OptimalPower), fmt.Sprintf("%.2f", data.CurrentPower), strconv.Itoa(int(data.TargetReplicas)), strconv.Itoa(int(data.ActualReplicas)), strconv.Itoa(int(data.Error))}
			records = append(records, record)
		}
		Expect(saveDataToCSV("solar_data.csv", append([][]string{header}, records...))).To(Succeed())
		
		// ... (Le résumé imprimé à la console reste utile) ...
		// fmt.Printf("Solar Cycle Summary:\n")
		// fmt.Printf("Total Steps: %d\n", len(collectedData))
		// fmt.Printf("Total Power Fluctuations: %d\n", len(collectedData))
		// fmt.Printf("Average Replication Error: %.2f\n", calculateAverageReplicationError(collectedData))
		// fmt.Printf("Peak Power: %.2f\n", findPeakPower(collectedData))
		// fmt.Printf("Minimum Power: %.2f\n", findMinimumPower(collectedData))
		// fmt.Printf("Final Replicas: %v\n", expectedInitialReplicas)
		// fmt.Printf("Data saved to solar_data.csv\n")
	})
})


// *** HELPER FUNCTION IS NOW DEFINITIVELY CORRECTED ***
func updatePowerAndCollectSolarData(ctx context.Context, powerFactor, optimalPower float64, data *[]RampDataPoint, scaler *DeclarativeScaler, namespace string) {
	currentPower := optimalPower * powerFactor
	
	By(fmt.Sprintf("Setting power to %.2f (%.0f%%)", currentPower, powerFactor*100))
	
	nodeList := &corev1.NodeList{}
	Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "solar"})).Should(Succeed())
	
	// THE FIX: Iterate by index to get a pointer and avoid stale object updates.
	for i := range nodeList.Items {
		nodeToUpdate := &nodeList.Items[i] // This is a pointer to the original object in the slice.
		opt, _ := strconv.ParseFloat(nodeToUpdate.Labels[optimalPowerLabel], 64)
		nodeToUpdate.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", opt*powerFactor)
		Expect(k8sClient.Update(ctx, nodeToUpdate)).Should(Succeed())
	}
	
	time.Sleep(StepDelay)
	
	dataPoint := collectDataPoint(ctx, scaler, namespace, currentPower, optimalPower)
	*data = append(*data, dataPoint)
}