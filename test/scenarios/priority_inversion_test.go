// Package scenarios contains long-running tests that simulate real-world conditions
// to validate the dynamic behavior of the Elara controller.
package scenarios

import (
	//"context"
	"fmt"
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

var _ = Describe("Scenario: Priority Inversion Test", func() {

	const (
		scenarioTimeout    = time.Minute * 5
		tickInterval       = time.Second * 2
		simulationDuration = time.Minute * 3 // A 3-minute simulation to show all phases
		namespace          = "default"
	)

	It("should prioritize scaling based on capacity (up) and consumption (down)", func(ctx SpecContext) {
		By("Setting up two deployments with imbalanced replica counts")

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "scenario-node-priority",
				Annotations: map[string]string{"elara.dev/power-consumption": "200.0"}, // Start at a stable medium power
			},
		}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())

		// large-deployment: High consumption (15 replicas), low available capacity (20-15=5)
		largeDep := createDeployment(ctx, k8sClient, "large-deployment", namespace, 15)
		// small-deployment: Low consumption (3 replicas), high available capacity (20-3=17)
		smallDep := createDeployment(ctx, k8sClient, "small-deployment", namespace, 3)

		largeDepKey := client.ObjectKeyFromObject(largeDep)
		smallDepKey := client.ObjectKeyFromObject(smallDep)
		// Initial total replicas = 15 + 3 = 18

		// A reactive controller configuration to clearly show the effects.
		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "priority-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.05"), // Sensitive deadband
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 6 * time.Second}, // Reactive windows
					Decrease:          metav1.Duration{Duration: 8 * time.Second},
					IncreaseTolerance: resource.MustParse("0.30"), // Tolerant for ramps
					DecreaseTolerance: resource.MustParse("0.30"),
				},
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "large-deployment", Namespace: namespace, MinReplicas: 2, MaxReplicas: 20}},
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "small-deployment", Namespace: namespace, MinReplicas: 2, MaxReplicas: 20}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

		collector := NewCollector()

		By("Running the simulation loop")
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()

		endTime := time.Now().Add(simulationDuration)

		for now := range ticker.C {
			if now.After(endTime) {
				break
			}

			// --- Define the Power Signal: Ramp Up -> Plateau -> Ramp Down ---
			elapsedSeconds := now.Sub(collector.startTime).Seconds()
			currentPower := 200.0 // Base power

			if elapsedSeconds < 75 {
				// Phase 1: Ramp up from 200W to 400W over 75 seconds
				currentPower += (elapsedSeconds / 75.0) * 200.0
			} else if elapsedSeconds < 105 {
				// Phase 2: Plateau at 400W for 30 seconds
				currentPower = 400.0
			} else {
				// Phase 3: Ramp down from 400W to 150W over the remaining time
				remainingDuration := simulationDuration.Seconds() - 105.0
				progressInPhase := elapsedSeconds - 105.0
				currentPower = 400.0 - (progressInPhase/remainingDuration)*250.0
			}

			// --- Update Cluster State ---
			nodeKey := client.ObjectKeyFromObject(node)
			Eventually(ctx, func() error {
				err := k8sClient.Get(ctx, nodeKey, node)
				if err != nil {
					return err
				}
				node.Annotations["elara.dev/power-consumption"] = fmt.Sprintf("%.2f", currentPower)
				return k8sClient.Update(ctx, node)
			}, "5s", "100ms").Should(Succeed())

			// --- Collect Data for Analysis ---
			scalerKey := client.ObjectKeyFromObject(scaler)
			fetchedScaler := &scalingv1alpha1.ElaraScaler{}
			Expect(k8sClient.Get(ctx, scalerKey, fetchedScaler)).Should(Succeed())

			fetchedLargeDep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, largeDepKey, fetchedLargeDep)).Should(Succeed())
			fetchedSmallDep := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, smallDepKey, fetchedSmallDep)).Should(Succeed())

			refPower := 0.0
			if fetchedScaler.Status.ReferencePower != nil {
				refPower = fetchedScaler.Status.ReferencePower.AsApproximateFloat64()
			}

			replicas := map[string]int{
				"large-deployment": int(*fetchedLargeDep.Spec.Replicas),
				"small-deployment": int(*fetchedSmallDep.Spec.Replicas),
			}
			collector.Collect(currentPower, refPower, fetchedScaler.Status.Mode, replicas)
		}

		By("Exporting collected data to CSV")
		err := collector.ExportToCSV("priority_inversion_results.csv")
		Expect(err).NotTo(HaveOccurred())

		By("Cleaning up resources")
		Expect(k8sClient.Delete(ctx, scaler)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, largeDep)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, smallDep)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, node)).Should(Succeed())

	}, SpecTimeout(scenarioTimeout))
})
