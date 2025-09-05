package scenarios

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	scalingv1alpha1 "elara/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	//"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	//"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//const scenarioTimeout = time.Minute * 5

// Helper to create a standard deployment for tests
func createDeployment(ctx context.Context, k8sClient client.Client, name string, namespace string, replicas int32) *appsv1.Deployment {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": name},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "main",
						Image: "nginx:latest",
					}},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
	return dep
}

var _ = Describe("Scenario: Full-Cycle Ramp", func() {

	const (
		scenarioTimeout    = time.Minute * 5
		tickInterval       = time.Second * 2
		simulationDuration = time.Minute * 2
		namespace          = "default"
	)

	It("should scale replicas up and down smoothly following a power ramp", func(ctx SpecContext) {
		By("Setting up the test environment")
		//ctx := context.Background()

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "scenario-node-1",
				Annotations: map[string]string{"elara.dev/power-consumption": "100.0"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())

		dep1 := createDeployment(ctx, k8sClient, "dep1", namespace, 5)
		dep2 := createDeployment(ctx, k8sClient, "dep2", namespace, 5)
		dep1Key := client.ObjectKeyFromObject(dep1)
		dep2Key := client.ObjectKeyFromObject(dep2)

		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "ramp-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.05"), // 5% deadband
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 4 * time.Second},
					Decrease:          metav1.Duration{Duration: 6 * time.Second},
					IncreaseTolerance: resource.MustParse("0.30"), // 25%
					DecreaseTolerance: resource.MustParse("0.50"),
				},
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "dep1", Namespace: namespace, MinReplicas: 1, MaxReplicas: 20}},
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "dep2", Namespace: namespace, MinReplicas: 1, MaxReplicas: 20}},
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

			elapsedSeconds := now.Sub(collector.startTime).Seconds()
			currentPower := 100.0

			if elapsedSeconds < 60 {
				currentPower += elapsedSeconds * 2.0 // Ramp up from 100W to 220W
			} else if elapsedSeconds < 90 {
				currentPower = 220.0 // Plateau at 220W
			} else {
				currentPower = 220.0 - (elapsedSeconds-90)*4.0 // Ramp down
			}

			if currentPower < 50.0 {
				currentPower = 50.0
			}

			nodeKey := client.ObjectKeyFromObject(node)
			Expect(k8sClient.Get(ctx, nodeKey, node)).Should(Succeed())
			node.Annotations["elara.dev/power-consumption"] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			scalerKey := client.ObjectKeyFromObject(scaler)
			fetchedScaler := &scalingv1alpha1.ElaraScaler{}
			Expect(k8sClient.Get(ctx, scalerKey, fetchedScaler)).Should(Succeed())

			fetchedDep1 := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, dep1Key, fetchedDep1)).Should(Succeed())
			fetchedDep2 := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, dep2Key, fetchedDep2)).Should(Succeed())

			refPower := 0.0
			if fetchedScaler.Status.ReferencePower != nil {
				refPower = fetchedScaler.Status.ReferencePower.AsApproximateFloat64()
			}

			// Create a map for replicas
			replicas := map[string]int{
				"dep1": int(*fetchedDep1.Spec.Replicas),
				"dep2": int(*fetchedDep2.Spec.Replicas),
			}

			// Call the new Collect function
			collector.Collect(currentPower, refPower, fetchedScaler.Status.Mode, replicas)
		}

		By("Exporting collected data to CSV")
		err := collector.ExportToCSV("full_cycle_ramp_results.csv")
		Expect(err).NotTo(HaveOccurred())

		By("Cleaning up resources")
		Expect(k8sClient.Delete(ctx, scaler)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, dep1)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, dep2)).Should(Succeed())
		Expect(k8sClient.Delete(ctx, node)).Should(Succeed())

		// Final assertion to ensure data was collected
		Expect(len(collector.DataPoints)).Should(BeNumerically(">", 10))
	}, SpecTimeout(scenarioTimeout))
})
