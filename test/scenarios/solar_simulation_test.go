// Package scenarios contains long-running tests that simulate real-world conditions
// to validate the dynamic behavior of the Elara controller.
package scenarios

import (
	//"context"
	"fmt"
	"math"
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



var _ = Describe("Scenario: Solar Simulation Test", func() {

	const (
		scenarioTimeout    = time.Minute * 5
		tickInterval       = time.Second * 2
		simulationDuration = time.Minute * 3 // 3 minutes to simulate a full day's solar cycle
		namespace          = "default"
	)

	It("should track a solar-like power curve with minor fluctuations", func(ctx SpecContext) {
		By("Setting up the test environment for solar simulation")

		// Use the modern, non-deprecated method for creating a local random generator.
		// This ensures our simulation is reproducible if needed, but is seeded randomly by default here.
		source := rand.NewSource(time.Now().UnixNano())
		localRand := rand.New(source)

		// Create the single node that will report the solar power signal.
		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "scenario-node-solar",
				Annotations: map[string]string{"elara.dev/power-consumption": "60.0"}, // Start at low power (sunrise)
			},
		}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())

		// Create two standard deployments to share the load.
		depA := createDeployment(ctx, k8sClient, "app-a", namespace, 3)
		depB := createDeployment(ctx, k8sClient, "app-b", namespace, 3)
		depAKey := client.ObjectKeyFromObject(depA)
		depBKey := client.ObjectKeyFromObject(depB)

		// Create the ElaraScaler with balanced parameters suitable for a slow-moving signal.
		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "solar-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.05"), // An 5% deadband helps ignore minor noise.
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 6 * time.Second}, // Reasonably slow to ensure stability on ramp-up.
					Decrease:          metav1.Duration{Duration: 10 * time.Second}, // Slower on ramp-down to be conservative.
					IncreaseTolerance: resource.MustParse("0.30"), // Allow 30% signal variation during the window.
					DecreaseTolerance: resource.MustParse("0.30"),
				},
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "app-a", Namespace: namespace, MinReplicas: 2, MaxReplicas: 25}},
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "app-b", Namespace: namespace, MinReplicas: 2, MaxReplicas: 25}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

		// Initialize the data collector.
		collector := NewCollector()

		By("Running the solar simulation loop")
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()

		endTime := time.Now().Add(simulationDuration)

		for now := range ticker.C {
			if now.After(endTime) {
				break
			}

			// --- Generate the Sinusoidal Solar Power Signal ---
			elapsedSeconds := now.Sub(collector.startTime).Seconds()
			simulationLength := simulationDuration.Seconds()

			// 1. Base Sinusoidal Curve:
			// We map the simulation duration to half a sine wave (from 0 to PI radians).
			// This creates a smooth curve that starts at 0, peaks in the middle, and ends at 0.
			// sin(0) = 0, sin(PI/2) = 1 (midday), sin(PI) = 0.
			baseSineValue := math.Sin((elapsedSeconds / simulationLength) * math.Pi)
			// We scale this sine value to a realistic power range.
			// The base power is 50W (night). The peak adds 200W, for a max of 250W at midday.
			powerFromSun := 50.0 + baseSineValue*200.0

			// 2. High-Frequency Noise:
			// We add small, random fluctuations to simulate atmospheric changes.
			// localRand.Float64() returns a value in [0.0, 1.0). Subtracting 0.5 centers it around 0.
			// This creates random noise between -5W and +5W.
			noise := (localRand.Float64() - 0.5) * 10.0
			powerWithNoise := powerFromSun + noise

			// 3. Cloud Simulation:
			// We introduce two significant, temporary power drops to simulate clouds passing over.
			// These test the controller's response to sudden but temporary dips.
			powerWithClouds := powerWithNoise
			// A cloud appears between t=60s and t=80s, causing a 40W drop.
			if elapsedSeconds > 60 && elapsedSeconds < 80 {
				powerWithClouds -= 40.0
			}
			// A second, larger cloud appears between t=130s and t=145s, causing a 50W drop.
			if elapsedSeconds > 130 && elapsedSeconds < 145 {
				powerWithClouds -= 50.0
			}

			// Final Power Signal: Ensure power never drops below the baseline.
			currentPower := math.Max(50.0, powerWithClouds)

			// --- Update Cluster State ---
			nodeKey := client.ObjectKeyFromObject(node)
			Expect(k8sClient.Get(ctx, nodeKey, node)).Should(Succeed())
			node.Annotations["elara.dev/power-consumption"] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			// --- Collect Data for Analysis ---
			scalerKey := client.ObjectKeyFromObject(scaler)
			fetchedScaler := &scalingv1alpha1.ElaraScaler{}
			Expect(k8sClient.Get(ctx, scalerKey, fetchedScaler)).Should(Succeed())

			fetchedDepA := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, depAKey, fetchedDepA)).Should(Succeed())
			fetchedDepB := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, depBKey, fetchedDepB)).Should(Succeed())

			refPower := 0.0
			if fetchedScaler.Status.ReferencePower != nil {
				refPower = fetchedScaler.Status.ReferencePower.AsApproximateFloat64()
			}

			replicas := map[string]int{
				"app-a": int(*fetchedDepA.Spec.Replicas),
				"app-b": int(*fetchedDepB.Spec.Replicas),
			}
			collector.Collect(currentPower, refPower, fetchedScaler.Status.Mode, replicas)
		}

		By("Exporting collected data to CSV")
		err := collector.ExportToCSV("solar_simulation_results.csv")
		Expect(err).NotTo(HaveOccurred())

		By("Cleaning up resources")
		Expect(k8sClient.Delete(ctx, scaler)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, depA)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, depB)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, node)).Should(Succeed())

	}, SpecTimeout(scenarioTimeout))
})