// Package scenarios contains long-running tests that simulate real-world conditions
// to validate the dynamic behavior of the Elara controller.
package scenarios

import (
	//"context"
	"fmt"
	"math/rand"
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

var _ = Describe("Scenario: High-Frequency Noise Test", func() {

	const (
		scenarioTimeout    = time.Minute * 3
		tickInterval       = time.Second * 1 // Use very fast ticks to create high-frequency noise
		simulationDuration = time.Minute * 2
		namespace          = "default"
	)

	It("should remain stable and perform no scaling actions under a noisy power signal", func(ctx SpecContext) {
		By("Setting up the test environment with a stable average power")

		// Use the modern, non-deprecated method for creating a local random generator.
		source := rand.NewSource(time.Now().UnixNano())
		localRand := rand.New(source)

		// The node will report a fluctuating power signal.
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "scenario-node-noise",
				Annotations: map[string]string{"elara.dev/power-consumption": "500.0"}, // Start at the average power
			},
		}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())

		// Setup a couple of standard deployments.
		depC := createDeployment(ctx, k8sClient, "app-c", namespace, 10)
		depD := createDeployment(ctx, k8sClient, "app-d", namespace, 10)
		depCKey := client.ObjectKeyFromObject(depC)
		depDKey := client.ObjectKeyFromObject(depD)
		// Initial total replicas = 20

		// A controller with production-like, conservative settings.
		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "noise-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.05"), // A 5% deadband, which will be frequently crossed.
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 10 * time.Second}, // A standard 10s window.
					Decrease:          metav1.Duration{Duration: 10 * time.Second},
					IncreaseTolerance: resource.MustParse("0.08"), // A tight 8% tolerance.
					DecreaseTolerance: resource.MustParse("0.08"),
				},
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "app-c", Namespace: namespace, MinReplicas: 5, MaxReplicas: 20}},
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "app-d", Namespace: namespace, MinReplicas: 5, MaxReplicas: 20}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

		collector := NewCollector()
		initialReplicas := 20 // The starting total replica count

		By("Running the simulation loop with a high-frequency noise signal")
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()

		endTime := time.Now().Add(simulationDuration)

		for now := range ticker.C {
			if now.After(endTime) {
				break
			}

			// --- Generate the Noisy Power Signal ---
			averagePower := 500.0
			// Create large fluctuations, e.g., +/- 100W (20% of the average).
			// This ensures the deadband is always triggered.
			noiseAmplitude := 100.0
			noise := (localRand.Float64() - 0.5) * 2 * noiseAmplitude // Fluctuation from -100 to +100
			currentPower := averagePower + noise

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

			fetchedDepC := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, depCKey, fetchedDepC)).Should(Succeed())
			fetchedDepD := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, depDKey, fetchedDepD)).Should(Succeed())

			refPower := 0.0
			if fetchedScaler.Status.ReferencePower != nil {
				refPower = fetchedScaler.Status.ReferencePower.AsApproximateFloat64()
			}

			replicas := map[string]int{
				"app-c": int(*fetchedDepC.Spec.Replicas),
				"app-d": int(*fetchedDepD.Spec.Replicas),
			}
			collector.Collect(currentPower, refPower, fetchedScaler.Status.Mode, replicas)
		}

		By("Exporting collected data to CSV")
		err := collector.ExportToCSV("high_frequency_noise_results.csv")
		Expect(err).NotTo(HaveOccurred())

		// --- Final Assertion ---
		// The most important check: verify that no scaling action was ever taken.
		// Every data point should have the same total number of replicas.
		By("Verifying that the total number of replicas never changed")
		for _, dp := range collector.DataPoints {
			Expect(dp.TotalReplicas).To(Equal(initialReplicas))
		}

		By("Cleaning up resources")
		Expect(k8sClient.Delete(ctx, scaler)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, depC)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, depD)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, node)).Should(Succeed())

	}, SpecTimeout(scenarioTimeout))
})
