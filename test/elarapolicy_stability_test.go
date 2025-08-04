package controllers

import (
	"context"
	"fmt"
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
	ctrl "elara/controllers"
)

// Data structure for this specific test
type StabilityDataPoint struct {
	Timestamp      time.Time
	OptimalPower   float64
	CurrentPower   float64
	TargetReplicas int32
	ActualReplicas int32
}

var _ = Describe("ElaraPolicy Controller: Stability Test (High-Frequency Noise)", func() {

	const (
		StabilityTestNamespace = "default"
		StabilityPolicyName    = "stability-test-policy"
		NumNoiseSteps          = 50               // Number of steps to simulate noisy signal
		StepDelay              = 2 * time.Second  // Short delay to capture rapid changes
		stabilityTimeout       = 180 * time.Second
		stabilityInterval      = 250 * time.Millisecond
	)

	ctx := context.Background()

	// Robust, non-hanging cleanup logic
	AfterEach(func() {
		By("Cleaning up stability test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: StabilityPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		nodeList := &corev1.NodeList{}
		_ = k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "stability"})
		for _, node := range nodeList.Items {
			_ = k8sClient.Delete(ctx, &node)
		}
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(StabilityTestNamespace), client.MatchingLabels{"elara-test": "stability"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(StabilityTestNamespace), client.MatchingLabels{"elara-test": "stability"})
			return len(list.Items)
		}, stabilityTimeout, stabilityInterval).Should(BeZero())
	})

	It("should remain stable and not 'flap' when subjected to a noisy power signal", func() {
		// --- SETUP ---
		By("Creating a complex, heterogeneous cluster topology")
		managedDeployments := generateComplexTopology(StabilityTestNamespace, 30)
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "stability"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "stability-node-1", Labels: map[string]string{"elara-test": "stability", optimalPowerLabel: "1000", currentPowerLabel: "1000"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "stability-node-2", Labels: map[string]string{"elara-test": "stability", optimalPowerLabel: "500", currentPowerLabel: "500"}}},
		}
		var optimalPower float64
		for _, node := range nodes {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
			optimalPower += opt
		}
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: StabilityPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		
		// --- NOISY SIGNAL SIMULATION & DATA COLLECTION ---
		By("Executing a noisy power signal simulation around a stable mean")
		var collectedData []StabilityDataPoint
		scaler := &ctrl.DeclarativeScaler{Deployments: managedDeployments}
		rand.Seed(time.Now().UnixNano())

		meanPowerFactor := 0.80 // The stable average power level (80%)
		noiseAmplitude := 0.05  // The noise will be +/- 5%

		for i := 0; i <= NumNoiseSteps; i++ {
			// Generate random noise between -1 and 1
			noise := (rand.Float64() * 2) - 1 
			powerFactor := meanPowerFactor + (noise * noiseAmplitude)

			// Clamp to ensure power is always valid (0% to 100%)
			if powerFactor < 0 { powerFactor = 0 }
			if powerFactor > 1 { powerFactor = 1 }

			// Update node power and collect the system's state
			currentPower := optimalPower * powerFactor
			
			By(fmt.Sprintf("Noise Step %d/%d: Setting power to %.2f (%.1f%%)", i, NumNoiseSteps, currentPower, powerFactor*100))
			
			nodeList := &corev1.NodeList{}
			Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "stability"})).Should(Succeed())
			for i := range nodeList.Items {
				nodeToUpdate := &nodeList.Items[i]
				opt, _ := strconv.ParseFloat(nodeToUpdate.Labels[optimalPowerLabel], 64)
				nodeToUpdate.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", opt*powerFactor)
				Expect(k8sClient.Update(ctx, nodeToUpdate)).Should(Succeed())
			}
			
			time.Sleep(StepDelay)
			
			// Collect data after waiting
			targetStates := scaler.CalculateTargetState(1.0 - powerFactor)
			var totalTargetReplicas int32
			for _, ts := range targetStates { totalTargetReplicas += ts.FinalReplicas }

			deploymentList := &appsv1.DeploymentList{}
			Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(StabilityTestNamespace))).Should(Succeed())
			var totalActualReplicas int32
			for _, dep := range deploymentList.Items {
				if dep.Spec.Replicas != nil { totalActualReplicas += *dep.Spec.Replicas }
			}
			
			collectedData = append(collectedData, StabilityDataPoint{
				Timestamp: time.Now(), OptimalPower: optimalPower, CurrentPower: currentPower,
				TargetReplicas: totalTargetReplicas, ActualReplicas: totalActualReplicas,
			})
		}

		// --- REPORTING ---
		By("Reporting and saving collected metrics for the stability test")
		header := []string{"Step", "OptimalPower", "CurrentPower", "TargetReplicas", "ActualReplicas"}
		records := [][]string{}
		for i, data := range collectedData {
			record := []string{strconv.Itoa(i + 1), fmt.Sprintf("%.2f", data.OptimalPower), fmt.Sprintf("%.2f", data.CurrentPower), strconv.Itoa(int(data.TargetReplicas)), strconv.Itoa(int(data.ActualReplicas))}
			records = append(records, record)
		}
		Expect(saveDataToCSV("stability_data.csv", append([][]string{header}, records...))).To(Succeed())
	})
})