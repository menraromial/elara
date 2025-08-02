package controllers

import (
	"context"
	"fmt"
	"math"
	"time"

	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

// DataPoint for this specific test
type SensitivityDataPoint struct {
	ConstraintLevel      int // minReplicas as a percentage of maxReplicas (e.g., 10 for 10%)
	TargetReduction      int
	AchievedReduction    int
	UnrealizedReduction  float64 // Percentage of reduction that could not be achieved
}

var _ = Describe("ElaraPolicy Controller: Constraint Sensitivity Test", func() {

	const (
		SensitivityNamespace = "default"
		SensitivityPolicyName= "sensitivity-test-policy"
		NodeName             = "sensitivity-test-node"
		sensitivityTimeout   = 120 * time.Second
		sensitivityInterval  = 250 * time.Millisecond
	)

	ctx := context.Background()

	// Robust, non-hanging cleanup logic
	AfterEach(func() {
		By("Cleaning up sensitivity test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: SensitivityPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(SensitivityNamespace), client.MatchingLabels{"elara-test": "sensitivity"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(SensitivityNamespace), client.MatchingLabels{"elara-test": "sensitivity"})
			return len(list.Items)
		}, sensitivityTimeout, sensitivityInterval).Should(BeZero())
	})

	It("should show degrading reduction effectiveness as constraints tighten", func() {
		var collectedData []SensitivityDataPoint
		
		// The experiment will loop from a low constraint level to a high one
		constraintLevels := []int{10, 20, 30, 40, 50, 60, 70, 80, 90}
		
		for _, level := range constraintLevels {
			By(fmt.Sprintf("--- Running Experiment for Constraint Level: %d%% ---", level))
			
			// --- SETUP for this iteration ---
			managedDeployments := generateConstrainedDeployments(SensitivityNamespace, 20, level)
			var totalMaxReplicas int32
			for _, md := range managedDeployments {
				totalMaxReplicas += md.MaxReplicas
				dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
				dep.Labels = map[string]string{"elara-test": "sensitivity"}
				Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
			}

			optimalPower := 1000.0
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: fmt.Sprintf("%.f", optimalPower), currentPowerLabel: fmt.Sprintf("%.f", optimalPower)}}}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())

			policy := &greenopsv1.ElaraPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: SensitivityPolicyName},
				Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
			}
			Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

			// --- ACTION ---
			By(fmt.Sprintf("Applying a 40%% power drop at %d%% constraint level", level))
			powerDropPercentage := 0.40
			currentPower := optimalPower * (1.0 - powerDropPercentage)

			// Calculate the theoretical target reduction before applying the change
			targetReduction := int(math.Ceil(float64(totalMaxReplicas) * powerDropPercentage))

			// Trigger the change
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
			node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())
			
			// --- MEASUREMENT ---
			// Wait for the system to converge
			scaler := &DeclarativeScaler{Deployments: managedDeployments}
			targetStates := scaler.CalculateTargetState(powerDropPercentage)
			expectedReplicas := make(map[string]int32)
			for _, ts := range targetStates {
				expectedReplicas[ts.Name] = ts.FinalReplicas
			}
			assertAllDeploymentsConverged(ctx, SensitivityNamespace, expectedReplicas)

			// Now measure the actual outcome
			deploymentList := &appsv1.DeploymentList{}
			Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(SensitivityNamespace))).Should(Succeed())
			var finalTotalReplicas int32
			for _, dep := range deploymentList.Items {
				finalTotalReplicas += *dep.Spec.Replicas
			}
			
			achievedReduction := int(totalMaxReplicas - finalTotalReplicas)
			unrealizedReduction := 0.0
			if targetReduction > 0 {
				unrealizedReduction = float64(targetReduction - achievedReduction) / float64(targetReduction) * 100.0
			}

			collectedData = append(collectedData, SensitivityDataPoint{
				ConstraintLevel: level, TargetReduction: targetReduction,
				AchievedReduction: achievedReduction, UnrealizedReduction: unrealizedReduction,
			})
			
			// --- TEARDOWN for this iteration ---
			By(fmt.Sprintf("Cleaning up iteration for %d%% constraint level", level))
			Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(SensitivityNamespace), client.MatchingLabels{"elara-test": "sensitivity"})).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
			Eventually(func() int {
				list := &appsv1.DeploymentList{}
				_ = k8sClient.List(ctx, list, client.InNamespace(SensitivityNamespace), client.MatchingLabels{"elara-test": "sensitivity"})
				return len(list.Items)
			}, sensitivityTimeout, sensitivityInterval).Should(BeZero())
		}
		
		// --- FINAL REPORTING ---
		By("Reporting and saving collected sensitivity metrics")
		header := []string{"ConstraintLevel", "TargetReduction", "AchievedReduction", "UnrealizedReduction_Percent"}
		records := [][]string{}
		for _, data := range collectedData {
			record := []string{strconv.Itoa(data.ConstraintLevel), strconv.Itoa(data.TargetReduction), strconv.Itoa(data.AchievedReduction), fmt.Sprintf("%.2f", data.UnrealizedReduction)}
			records = append(records, record)
		}
		Expect(saveDataToCSV("sensitivity_data.csv", append([][]string{header}, records...))).To(Succeed())
	})
})

// New helper function to generate deployments with a specific constraint level
func generateConstrainedDeployments(namespace string, count int, constraintPercent int) []greenopsv1.ManagedDeployment {
	deployments := make([]greenopsv1.ManagedDeployment, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("app-%d-lvl%d", i, constraintPercent)
		maxReplicas := int32(10 + (i % 10)) // Vary max replicas from 10 to 19
		// Calculate min replicas based on the constraint level percentage
		minReplicas := int32(math.Ceil(float64(maxReplicas) * (float64(constraintPercent) / 100.0)))
		if minReplicas < 1 { minReplicas = 1 } // Ensure min is at least 1
		
		deployments[i] = greenopsv1.ManagedDeployment{
			Name: name, Namespace: namespace, MinReplicas: minReplicas, MaxReplicas: maxReplicas,
		}
	}
	return deployments
}