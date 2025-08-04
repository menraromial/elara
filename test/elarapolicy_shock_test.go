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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
	ctrl "elara/controllers"
)

var _ = Describe("ElaraPolicy Controller: Large-Scale Shock & Recovery Test", func() {

	const (
		ShockTestNamespace = "default"
		ShockPolicyName    = "shock-test-policy"
		NodeName           = "shock-test-node"
		NumDeployments     = 200
		shockTimeout       = 180 * time.Second
		// High-frequency sampling
		SamplingDuration = 8 * time.Second 
		SamplingInterval = 250 * time.Millisecond
	)

	ctx := context.Background()

	// Robust, non-hanging cleanup
	AfterEach(func() {
		By("Cleaning up shock test resources")
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: ShockPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(ShockTestNamespace), client.MatchingLabels{"elara-test": "shock"})).Should(Succeed())
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(ShockTestNamespace), client.MatchingLabels{"elara-test": "shock"})
			return len(list.Items)
		}, shockTimeout, SamplingInterval).Should(BeZero())
	})

	It("should show a clear convergence curve when subjected to a large-scale shock", func() {
		// --- SETUP ---
		By(fmt.Sprintf("Creating %d deployments for a large-scale test", NumDeployments))
		managedDeployments := generateComplexTopology(ShockTestNamespace, NumDeployments)
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			dep.Labels = map[string]string{"elara-test": "shock"}
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		optimalPower := 10000.0
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: fmt.Sprintf("%.f", optimalPower), currentPowerLabel: fmt.Sprintf("%.f", optimalPower)}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: ShockPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, ShockTestNamespace, expectedInitialReplicas)

		// --- SHOCK & RECOVERY SIMULATION ---
		var collectedData []RampDataPoint
		scaler := &ctrl.DeclarativeScaler{Deployments: managedDeployments}
		startTime := time.Now()

		// --- STEP 1: Capture the initial stable state (t=0) ---
		By("Capturing initial stable state at t=0")
		dataPoint := collectDataPoint(ctx, scaler, ShockTestNamespace, optimalPower, optimalPower)
		collectedData = append(collectedData, dataPoint)

		// --- STEP 2: Apply the shock ---
		By("Injecting a 90% power drop (shock event)")
		shockPower := optimalPower * 0.10
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
		node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", shockPower)
		Expect(k8sClient.Update(ctx, node)).Should(Succeed())
		
		// --- STEP 3: Sample the response to the shock ---
		By("Sampling system response to shock")
		numSamples := int(SamplingDuration / SamplingInterval)
		for i := 0; i < numSamples; i++ {
			time.Sleep(SamplingInterval)
			dataPoint := collectDataPoint(ctx, scaler, ShockTestNamespace, shockPower, optimalPower)
			collectedData = append(collectedData, dataPoint)
		}

		// --- STEP 4: Apply the recovery ---
		By("Injecting an instantaneous power recovery")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
		node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", optimalPower)
		Expect(k8sClient.Update(ctx, node)).Should(Succeed())

		// --- STEP 5: Sample the response to recovery ---
		By("Sampling system response to recovery")
		for i := 0; i < numSamples; i++ {
			time.Sleep(SamplingInterval)
			dataPoint := collectDataPoint(ctx, scaler, ShockTestNamespace, optimalPower, optimalPower)
			collectedData = append(collectedData, dataPoint)
		}

		// --- REPORTING ---
		By("Saving collected shock and recovery metrics")
		header := []string{"Time_ms", "CurrentPower", "TargetReplicas", "ActualReplicas"}
		records := [][]string{}
		for _, data := range collectedData {
			elapsedMs := data.Timestamp.Sub(startTime).Milliseconds()
			record := []string{
				strconv.FormatInt(elapsedMs, 10),
				fmt.Sprintf("%.2f", data.CurrentPower),
				strconv.Itoa(int(data.TargetReplicas)),
				strconv.Itoa(int(data.ActualReplicas)),
			}
			records = append(records, record)
		}
		Expect(saveDataToCSV("shock_recovery_data.csv", append([][]string{header}, records...))).To(Succeed())
	})
})