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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
	ctrl "elara/controllers"
)

// *** CONSTANTS MOVED HERE to be accessible by all functions in this file ***
const (
	StepDelay        = 4 * time.Second   // Time to wait between power changes
	fullRampTimeout  = 180 * time.Second // This is a long test, so a generous timeout
	fullRampInterval = 250 * time.Millisecond
)

var _ = Describe("ElaraPolicy Controller: Full-Cycle Ramp Test", func() {

	// Constants specific to this test suite
	const (
		FullRampNamespace = "default"
		FullRampPolicyName    = "full-ramp-policy"
		NumRampSteps      = 10 // Number of steps for ramp down and up
	)

	ctx := context.Background()

	// The robust AfterEach block ensures tests do not hang.
	AfterEach(func() {
		By("Cleaning up full-cycle ramp test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: FullRampPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())

		nodeList := &corev1.NodeList{}
		_ = k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "full-ramp"})
		for _, node := range nodeList.Items {
			_ = k8sClient.Delete(ctx, &node)
		}
		
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(FullRampNamespace), client.MatchingLabels{"elara-test": "full-ramp"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(FullRampNamespace), client.MatchingLabels{"elara-test": "full-ramp"})
			return len(list.Items)
		}, fullRampTimeout, fullRampInterval).Should(BeZero())
	})

	It("should track a full ramp-down and ramp-up power cycle", func() {
		// --- SETUP ---
		By("Creating a complex, heterogeneous cluster topology")
		managedDeployments := generateComplexTopology(FullRampNamespace, 30)
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "full-ramp"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "ramp-node-1", Labels: map[string]string{"elara-test": "full-ramp", optimalPowerLabel: "1000", currentPowerLabel: "1000"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ramp-node-2", Labels: map[string]string{"elara-test": "full-ramp", optimalPowerLabel: "500", currentPowerLabel: "500"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "ramp-node-3", Labels: map[string]string{"elara-test": "full-ramp", optimalPowerLabel: "250", currentPowerLabel: "250"}}},
		}
		var optimalPower float64
		for _, node := range nodes {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
			optimalPower += opt
		}
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: FullRampPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, FullRampNamespace, expectedInitialReplicas)

		// --- POWER CYCLE & DATA COLLECTION ---
		By("Executing a full ramp-down and ramp-up power cycle")
		var collectedData []RampDataPoint
		scaler := &ctrl.DeclarativeScaler{Deployments: managedDeployments}
		minPowerFactor := 0.4

		// PHASE 1: RAMP DOWN
		for i := 0; i <= NumRampSteps; i++ {
			powerFactor := 1.0 - ((1.0 - minPowerFactor) * (float64(i) / float64(NumRampSteps)))
			updatePowerAndCollectData(ctx, powerFactor, optimalPower, &collectedData, scaler, FullRampNamespace)
		}

		// PHASE 2: RAMP UP
		for i := 1; i <= NumRampSteps; i++ {
			powerFactor := minPowerFactor + ((1.0 - minPowerFactor) * (float64(i) / float64(NumRampSteps)))
			updatePowerAndCollectData(ctx, powerFactor, optimalPower, &collectedData, scaler, FullRampNamespace)
		}

		// --- FINAL VALIDATION & REPORTING ---
		By("Asserting final state has returned to max replicas")
		assertAllDeploymentsConverged(ctx, FullRampNamespace, expectedInitialReplicas)

		By("Reporting and saving collected metrics for the full cycle")
		header := []string{"Step", "OptimalPower", "CurrentPower", "TargetReplicas", "ActualReplicas", "ReplicationError"}
		records := [][]string{}
		for i, data := range collectedData {
			record := []string{strconv.Itoa(i + 1), fmt.Sprintf("%.2f", data.OptimalPower), fmt.Sprintf("%.2f", data.CurrentPower), strconv.Itoa(int(data.TargetReplicas)), strconv.Itoa(int(data.ActualReplicas)), strconv.Itoa(int(data.Error))}
			records = append(records, record)
		}
		Expect(saveDataToCSV("full_ramp_data.csv", append([][]string{header}, records...))).To(Succeed())
		
		// Print a summary to the console.
		var totalError int64
		for _, data := range collectedData { totalError += int64(data.Error) }
		avgError := float64(totalError) / float64(len(collectedData))
		fmt.Printf("\n--- METRICS: FULL RAMP TEST SUMMARY ---\n")
		fmt.Printf("Average Replication Error: %.2f\n", avgError)
		fmt.Printf("--------------------------------------\n")
		Expect(avgError).To(BeNumerically("<", 10))
	})
})

// updatePowerAndCollectData is a helper function to avoid code duplication for ramp steps.
func updatePowerAndCollectData(ctx context.Context, powerFactor, optimalPower float64, data *[]RampDataPoint, scaler *ctrl.DeclarativeScaler, namespace string) {
	currentPower := optimalPower * powerFactor
	
	By(fmt.Sprintf("Setting power to %.2f (%.0f%%)", currentPower, powerFactor*100))
	
	nodeList := &corev1.NodeList{}
	Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "full-ramp"})).Should(Succeed())
	for _, node := range nodeList.Items {
		// This creates a copy of the node object to modify
		nodeToUpdate := node
		opt, _ := strconv.ParseFloat(nodeToUpdate.Labels[optimalPowerLabel], 64)
		nodeToUpdate.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", opt*powerFactor)
		Expect(k8sClient.Update(ctx, &nodeToUpdate)).Should(Succeed())
	}
	
	// This uses the package-level constant, which is now visible.
	time.Sleep(StepDelay)
	
	dataPoint := collectDataPoint(ctx, scaler, namespace, currentPower, optimalPower)
	*data = append(*data, dataPoint)
}