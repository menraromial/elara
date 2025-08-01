package controllers

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	//"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)



var _ = Describe("ElaraPolicy Controller: Ramp Convergence Test", func() {

	const (
		RampConvTestNamespace = "elara-ramp-conv-test"
		RampConvPolicyName    = "ramp-conv-test-policy"
		NodeName              = "ramp-conv-test-node"
		NumRampSteps          = 10
		rampConvTimeout       = 60 * time.Second
		rampConvInterval      = 250 * time.Millisecond
		perfTimeout  = time.Second * 90
	    perfInterval = time.Second * 1
	)

	ctx := context.Background()

	BeforeEach(func() {
		By("Creating namespace for ramp convergence test")
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: RampConvTestNamespace}}
		Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
	})

	AfterEach(func() {
		By("Cleaning up performance test resources (excluding namespace itself)")

		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: RampConvPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "perf-node"}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())

		By("Explicitly deleting all mock deployments in namespace " + RampConvTestNamespace)
		// Use DeleteAllOf for efficiency
		Expect(k8sClient.DeleteAllOf(ctx, &appsv1.Deployment{}, client.InNamespace(RampConvTestNamespace), client.MatchingLabels{"elara-test": "perf"})).Should(Succeed())

		By("Waiting for all deployments to be deleted from namespace " + RampConvTestNamespace)
		Eventually(func() int {
			list := &appsv1.DeploymentList{}
			_ = k8sClient.List(ctx, list, client.InNamespace(RampConvTestNamespace), client.MatchingLabels{"elara-test": "perf"})
			return len(list.Items)
		}, perfTimeout, perfInterval).Should(BeZero())
	})

	It("should converge quickly at each step of a power ramp", func() {
		// --- SETUP ---
		managedDeployments := generateManagedDeployments(RampConvTestNamespace, 20)
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: "1000", currentPowerLabel: "1000"}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: RampConvPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())
		
		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, RampConvTestNamespace, expectedInitialReplicas)

		// --- RAMP DOWN & CONVERGENCE TIME MEASUREMENT ---
		By("Beginning ramp down and measuring convergence time at each step")
		var convergenceTimes []time.Duration
		scaler := &DeclarativeScaler{Deployments: managedDeployments}

		optimalPower := 1000.0
		minPower := 400.0
		powerStep := (optimalPower - minPower) / float64(NumRampSteps)

		for i := 1; i <= NumRampSteps; i++ {
			currentPower := optimalPower - (float64(i) * powerStep)
			
			// Calculate the new target state BEFORE changing the power
			reductionPercentage := (optimalPower - currentPower) / optimalPower
			targetStates := scaler.CalculateTargetState(reductionPercentage)
			expectedReplicas := make(map[string]int32)
			for _, ts := range targetStates {
				expectedReplicas[ts.Name] = ts.FinalReplicas
			}
			
			By(fmt.Sprintf("Ramp Down Step %d/%d: Setting power to %.2f", i, NumRampSteps, currentPower))
			
			// Start timer
			startTime := time.Now()
			
			// Trigger the change
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
			node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			// Wait for convergence and measure the time taken
			assertAllDeploymentsConverged(ctx, RampConvTestNamespace, expectedReplicas)
			convergenceTime := time.Since(startTime)
			convergenceTimes = append(convergenceTimes, convergenceTime)
		}

		// --- REPORTING ---
		By("Reporting collected metrics")
		var totalConvergenceMs int64
		var maxConvergenceMs int64
		fmt.Println("\n--- METRICS: RAMP CONVERGENCE TEST RESULTS ---")
		fmt.Println("Step,PowerLevel,ConvergenceTime(ms)")
		for i, ct := range convergenceTimes {
			powerLevel := optimalPower - (float64(i+1) * powerStep)
			ms := ct.Milliseconds()
			fmt.Printf("%d,%.2f,%d\n", i+1, powerLevel, ms)
			totalConvergenceMs += ms
			if ms > maxConvergenceMs {
				maxConvergenceMs = ms
			}
		}
		avgConvergenceMs := float64(totalConvergenceMs) / float64(len(convergenceTimes))
		fmt.Println("--- SUMMARY ---")
		fmt.Printf("Average Convergence Time: %.2f ms\n", avgConvergenceMs)
		fmt.Printf("Maximum Convergence Time: %d ms\n", maxConvergenceMs)
		fmt.Println("---------------------------------")

		Expect(maxConvergenceMs).To(BeNumerically("<", 5000), "Convergence at each step should be fast")
	})
})