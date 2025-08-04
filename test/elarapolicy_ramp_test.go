package controllers

import (
	"context"
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	//"k8s.io/apimachinery/pkg/api/errors"
	//"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	//"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
	ctrl "elara/controllers"
)

// Data structure to hold the metrics collected at each step of the ramp.
type RampDataPoint struct {
	Timestamp      time.Time
	OptimalPower   float64
	CurrentPower   float64
	TargetReplicas int32
	ActualReplicas int32
	Error          int32
}

var _ = Describe("ElaraPolicy Controller: Ramp Test", func() {

	const (
		ComplexRampNamespace = "default"
		ComplexRampPolicyName = "complex-ramp-policy"
		NumRampSteps          = 10
		StepDelay             = 5 * time.Second
		rampTimeout           = 90 * time.Second // Un peu plus de temps pour le setup/cleanup
		rampInterval          = 250 * time.Millisecond
	)

	ctx := context.Background()

	// BeforeEach: We no longer create a unique namespace per test.
	// We rely on 'default' or a fixed one that is assumed to exist.
	BeforeEach(func() {
		By(fmt.Sprintf("Using fixed namespace '%s' for ramp test (not explicitly created/deleted)", ComplexRampNamespace))
	})

	// AfterEach: This is the definitive cleanup logic that will prevent hangs.
	AfterEach(func() {
		By("Cleaning up complex ramp test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: ComplexRampPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())

		// Delete all test nodes
		nodeList := &corev1.NodeList{}
		Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "complex-ramp"})).Should(Succeed())
		for _, node := range nodeList.Items {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &node))).Should(Succeed())
		}
		
		// Delete all deployments in the namespace
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(ComplexRampNamespace), client.MatchingLabels{"elara-test": "complex-ramp"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(ComplexRampNamespace), client.MatchingLabels{"elara-test": "complex-ramp"})
			return len(list.Items)
		}, rampTimeout, rampInterval).Should(BeZero())
	})

	It("should track a gradual power signal in a complex, heterogeneous environment", func() {
		// --- SETUP ---
		// Generate a complex topology with multiple groups of deployments
		managedDeployments := generateComplexTopology(ComplexRampNamespace, 30)
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "complex-ramp"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}

		// Create multiple nodes with different capacities
		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "powerful-node-1", Labels: map[string]string{"elara-test": "complex-ramp", optimalPowerLabel: "1000", currentPowerLabel: "1000"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "medium-node-2",   Labels: map[string]string{"elara-test": "complex-ramp", optimalPowerLabel: "500", currentPowerLabel: "500"}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "small-node-3",    Labels: map[string]string{"elara-test": "complex-ramp", optimalPowerLabel: "250", currentPowerLabel: "250"}}},
		}
		var optimalPower float64
		for _, node := range nodes {
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
			optimalPower += opt
		}
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: ComplexRampPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, ComplexRampNamespace, expectedInitialReplicas)

		// --- RAMP DOWN & DATA COLLECTION ---
		By("Beginning ramp down from 100% to 40% power")
		var collectedData []RampDataPoint
		scaler := &ctrl.DeclarativeScaler{Deployments: managedDeployments}
		minPowerFactor := 0.4 // 40%
		
		for i := 0; i <= NumRampSteps; i++ {
			// Calculate the power factor for this step
			powerFactor := 1.0 - ( (1.0 - minPowerFactor) * (float64(i) / float64(NumRampSteps)) )
			currentPower := optimalPower * powerFactor

			By(fmt.Sprintf("Ramp Down Step %d/%d: Setting power to %.2f (%.0f%%)", i, NumRampSteps, currentPower, powerFactor*100))
			// Update the labels of all nodes proportionally
			nodeList := &corev1.NodeList{}
			Expect(k8sClient.List(ctx, nodeList, client.MatchingLabels{"elara-test": "complex-ramp"})).Should(Succeed())
			for _, node := range nodeList.Items {
				opt, _ := strconv.ParseFloat(node.Labels[optimalPowerLabel], 64)
				node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", opt * powerFactor)
				Expect(k8sClient.Update(ctx, &node)).Should(Succeed())
			}
			
			if i > 0 { time.Sleep(StepDelay) } // Pas de délai avant la première mesure

			dataPoint := collectDataPoint(ctx, scaler, ComplexRampNamespace, currentPower, optimalPower)
			collectedData = append(collectedData, dataPoint)
		}

		By("Reporting and saving collected metrics")
		var totalError int64
		var maxError int32
		fmt.Println("\n--- METRICS: RAMP TEST RESULTS ---")
		
		// --- THIS IS THE CORRECTED SECTION ---
		// The header now correctly matches the data being written.
		header := []string{"Step", "OptimalPower", "CurrentPower", "TargetReplicas", "ActualReplicas", "ReplicationError"}
		records := [][]string{header}
		for i, data := range collectedData {
			record := []string{
				strconv.Itoa(i + 1),
				fmt.Sprintf("%.2f", data.OptimalPower),
				fmt.Sprintf("%.2f", data.CurrentPower),
				strconv.Itoa(int(data.TargetReplicas)),
				strconv.Itoa(int(data.ActualReplicas)),
				strconv.Itoa(int(data.Error)),
			}
			records = append(records, record)
		}
		Expect(saveDataToCSV("ramp_error_data.csv", records)).To(Succeed())
		// --- END OF CORRECTION ---

		// Print the summary to the console for quick viewing.
		fmt.Println("Step,PowerLevel,TargetReplicas,ActualReplicas,ReplicationError")
		for i, data := range collectedData {
			fmt.Printf("%d,%.2f,%d,%d,%d\n", i+1, data.CurrentPower, data.TargetReplicas, data.ActualReplicas, data.Error)
			totalError += int64(data.Error)
			if data.Error > maxError {
				maxError = data.Error
			}
		}
		avgError := float64(totalError) / float64(len(collectedData))
		fmt.Println("--- SUMMARY ---")
		fmt.Printf("Average Replication Error: %.2f\n", avgError)
		fmt.Printf("Maximum Replication Error: %d\n", maxError)
		fmt.Println("---------------------------------")
		
		Expect(avgError).To(BeNumerically("<", 5), "Average replication error should be low")
	})
})

// collectDataPoint is a helper to measure the system's state at a point in time.
func collectDataPoint(ctx context.Context, scaler *ctrl.DeclarativeScaler, namespace string, currentPower, optimalPower float64) RampDataPoint {
	// 1. Calculate the theoretical target state
	reductionPercentage := (optimalPower - currentPower) / optimalPower
	targetStates := scaler.CalculateTargetState(reductionPercentage)
	var totalTargetReplicas int32
	for _, ts := range targetStates {
		totalTargetReplicas += ts.FinalReplicas
	}

	// 2. Get the actual state from the cluster
	deploymentList := &appsv1.DeploymentList{}
	Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(namespace))).Should(Succeed())
	var totalActualReplicas int32
	for _, dep := range deploymentList.Items {
		if dep.Spec.Replicas != nil {
			totalActualReplicas += *dep.Spec.Replicas
		}
	}

	// 3. Calculate error and create the data point
	errorVal := int32(math.Abs(float64(totalTargetReplicas - totalActualReplicas)))
	return RampDataPoint{
		Timestamp:      time.Now(),
		CurrentPower:   currentPower,
		OptimalPower:   optimalPower,
		TargetReplicas: totalTargetReplicas,
		ActualReplicas: totalActualReplicas,
		Error:          errorVal,
	}
}

// saveDataToCSV is a utility function to save collected data to a CSV file.
func saveDataToCSV(filename string, data [][]string) error {
	// Create the results directory if it doesn't exist
	if err := os.MkdirAll("../results", os.ModePerm); err != nil {
		return err
	}

	file, err := os.Create(filepath.Join("../results", filename))
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	for _, value := range data {
		if err := writer.Write(value); err != nil {
			return err
		}
	}
	return nil
}

// New helper function to create a richer topology
func generateComplexTopology(namespace string, count int) []greenopsv1.ManagedDeployment {
	deployments := make([]greenopsv1.ManagedDeployment, count)
	
	// Group 1: Backend Services (critical)
	for i := 0; i < count/3; i++ {
		name := fmt.Sprintf("backend-svc-%d", i)
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: 3, MaxReplicas: 10,
			Weight: resource.MustParse(fmt.Sprintf("%.1f", 1.0+float64(i))), Group: "backend-services",
		}
	}

	// Group 2: Frontend Services (less critical)
	start := count / 3
	for i := start; i < start+(count/3); i++ {
		name := fmt.Sprintf("frontend-app-%d", i)
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: 1, MaxReplicas: 20,
			Weight: resource.MustParse("1.0"), Group: "frontend-apps",
		}
	}

	// Independent Deployments (e.g., monitoring, batch jobs)
	start = 2 * (count / 3)
	for i := start; i < count; i++ {
		name := fmt.Sprintf("independent-job-%d", i)
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: 1, MaxReplicas: 5,
		}
	}
	
	return deployments
}