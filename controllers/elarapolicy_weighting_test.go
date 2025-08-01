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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

// DataPoint for this specific test, tracking individual deployments.
type WeightingDataPoint struct {
	Step               int
	CurrentPower       float64
	ReplicasCritical   int32
	ReplicasBestEffort int32
}

var _ = Describe("ElaraPolicy Controller: Weighting Effectiveness Test", func() {

	const (
		WeightingTestNamespace = "default"
		WeightingPolicyName    = "weighting-test-policy"
		NodeName               = "weighting-test-node"
		NumRampSteps           = 20
		StepDelay              = 3 * time.Second
		weightingTimeout       = 120 * time.Second
		weightingInterval      = 250 * time.Millisecond
	)

	ctx := context.Background()

	// La logique de nettoyage robuste est conservée
	AfterEach(func() {
		By("Cleaning up weighting test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: WeightingPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(WeightingTestNamespace), client.MatchingLabels{"elara-test": "weighting"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(WeightingTestNamespace), client.MatchingLabels{"elara-test": "weighting"})
			return len(list.Items)
		}, weightingTimeout, weightingInterval).Should(BeZero())
	})

	It("should prioritize scaling down low-weight services first", func() {
		// --- SETUP ---
		By("Creating a high-priority and a low-priority service")
		
		criticalServiceName := "critical-auth-service"
		bestEffortServiceName := "best-effort-analytics-service"

		managedDeployments := []greenopsv1.ManagedDeployment{
			{Name: criticalServiceName, Namespace: WeightingTestNamespace, MinReplicas: 1, MaxReplicas: 20, Weight: resource.MustParse("9.0"), Group: "api-suite"},
			{Name: bestEffortServiceName, Namespace: WeightingTestNamespace, MinReplicas: 1, MaxReplicas: 20, Weight: resource.MustParse("1.0"), Group: "api-suite"},
		}

		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "weighting"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		optimalPower := 1000.0
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: fmt.Sprintf("%.f", optimalPower), currentPowerLabel: fmt.Sprintf("%.f", optimalPower)}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: WeightingPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, WeightingTestNamespace, expectedInitialReplicas)

		// --- RAMP DOWN & DATA COLLECTION ---
		By("Executing a gradual ramp down to observe scaling trajectories")
		var collectedData []WeightingDataPoint
		minPowerFactor := 0.3

		for i := 0; i <= NumRampSteps; i++ {
			powerFactor := 1.0 - ((1.0 - minPowerFactor) * (float64(i) / float64(NumRampSteps)))
			currentPower := optimalPower * powerFactor
			
			By(fmt.Sprintf("Ramp Down Step %d/%d: Setting power to %.2f (%.0f%%)", i, NumRampSteps, currentPower, powerFactor*100))
			
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
			node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())
			
			time.Sleep(StepDelay)
			
			var criticalReplicas, bestEffortReplicas int32
			
			critDep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: criticalServiceName, Namespace: WeightingTestNamespace}, critDep)).Should(Succeed())
			criticalReplicas = *critDep.Spec.Replicas
			
			beDep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: bestEffortServiceName, Namespace: WeightingTestNamespace}, beDep)).Should(Succeed())
			bestEffortReplicas = *beDep.Spec.Replicas

			collectedData = append(collectedData, WeightingDataPoint{
				Step: i, CurrentPower: currentPower,
				ReplicasCritical: criticalReplicas, ReplicasBestEffort: bestEffortReplicas,
			})
		}

		// --- FINAL VALIDATION & REPORTING ---
		// *** THIS IS THE FINAL AND CORRECT ASSERTION BLOCK ***
		By("Asserting that the critical service retained more replicas")
		finalCriticalReplicas := collectedData[len(collectedData)-1].ReplicasCritical
		finalBestEffortReplicas := collectedData[len(collectedData)-1].ReplicasBestEffort

		// The core hypothesis of our experiment.
		Expect(finalCriticalReplicas).To(BeNumerically(">", finalBestEffortReplicas), "Critical service should have more replicas than the best-effort one at low power.")
		
		// The specific expected values based on the corrected inverse logic.
		// At 30% power (70% reduction), total reduction is 28 replicas.
		// Best-effort (weight 1.0) is targeted for most of the reduction until it hits its minimum.
		// Critical (weight 9.0) is protected and is reduced much less.
		Expect(finalCriticalReplicas).To(Equal(int32(11)))
		Expect(finalBestEffortReplicas).To(Equal(int32(1)))

		By("Reporting and saving collected metrics for the weighting test")
		header := []string{"Step", "CurrentPower", "ReplicasCritical", "ReplicasBestEffort"}
		records := [][]string{}
		for _, data := range collectedData {
			record := []string{strconv.Itoa(data.Step), fmt.Sprintf("%.2f", data.CurrentPower), strconv.Itoa(int(data.ReplicasCritical)), strconv.Itoa(int(data.ReplicasBestEffort))}
			records = append(records, record)
		}
		Expect(saveDataToCSV("weighting_data.csv", append([][]string{header}, records...))).To(Succeed())
	})
})