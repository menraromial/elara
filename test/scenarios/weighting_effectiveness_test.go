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



var _ = Describe("Scenario: Weighting Effectiveness Test", func() {

	const (
		scenarioTimeout    = time.Minute * 5
		tickInterval       = time.Second * 3 // Ticks un peu plus lents pour laisser le temps au scaling
		simulationDuration = time.Minute * 2
		namespace          = "default"
	)

	It("should distribute replicas according to weights and redistribute surplus when constrained", func(ctx SpecContext) {
		By("Setting up a weighted group and an independent deployment")

		node := &corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "scenario-node-weighting",
				Annotations: map[string]string{"elara.dev/power-consumption": "100.0"},
			},
		}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())

		// Group members
		apiSvc := createDeployment(ctx, k8sClient, "api-service", namespace, 2)
		worker := createDeployment(ctx, k8sClient, "worker", namespace, 2)
		// Independent deployment
		frontend := createDeployment(ctx, k8sClient, "frontend", namespace, 2)

		apiSvcKey := client.ObjectKeyFromObject(apiSvc)
		workerKey := client.ObjectKeyFromObject(worker)
		frontendKey := client.ObjectKeyFromObject(frontend)

		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "weighting-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.10"), // Deadband plus large
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 5 * time.Second}, // Réactif
					Decrease:          metav1.Duration{Duration: 10 * time.Second},
					IncreaseTolerance: resource.MustParse("0.30"),
					DecreaseTolerance: resource.MustParse("0.50"),
				},
				// Définition d'un groupe avec des poids et contraintes distincts
				DeploymentGroups: []scalingv1alpha1.DeploymentGroupSpec{
					{
						Name: "backend-group",
						Members: []scalingv1alpha1.GroupMemberSpec{
							{
								// Poids élevé, contrainte serrée
								Weight:               3,
								DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "api-service", Namespace: namespace, MinReplicas: 2, MaxReplicas: 10},
							},
							{
								// Poids faible, contrainte large
								Weight:               1,
								DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "worker", Namespace: namespace, MinReplicas: 2, MaxReplicas: 30},
							},
						},
					},
				},
				// Et un déploiement indépendant
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "frontend", Namespace: namespace, MinReplicas: 2, MaxReplicas: 15}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

		collector := NewCollector() // Nous devons adapter le collecteur

		By("Running the simulation loop with a steady power ramp-up")
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()

		endTime := time.Now().Add(simulationDuration)

		for now := range ticker.C {
			if now.After(endTime) {
				break
			}

			elapsedSeconds := now.Sub(collector.startTime).Seconds()
			// Simple rampe linéaire de 100W à 300W sur 120 secondes
			currentPower := 100.0 + (elapsedSeconds * (200.0 / 120.0))

			// Update node power
			nodeKey := client.ObjectKeyFromObject(node)
			Expect(k8sClient.Get(ctx, nodeKey, node)).Should(Succeed())
			node.Annotations["elara.dev/power-consumption"] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			scalerKey := client.ObjectKeyFromObject(scaler)
			fetchedScaler := &scalingv1alpha1.ElaraScaler{}
			Expect(k8sClient.Get(ctx, scalerKey, fetchedScaler)).Should(Succeed())

			fetchedApiSvc := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, apiSvcKey, fetchedApiSvc)).Should(Succeed())
			fetchedWorker := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, workerKey, fetchedWorker)).Should(Succeed())
			fetchedFrontend := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, frontendKey, fetchedFrontend)).Should(Succeed())

			refPower := 0.0
			if fetchedScaler.Status.ReferencePower != nil {
				refPower = fetchedScaler.Status.ReferencePower.AsApproximateFloat64()
			}

			// Créer la map avec nos trois déploiements
			replicas := map[string]int{
				"api-service": int(*fetchedApiSvc.Spec.Replicas),
				"worker":      int(*fetchedWorker.Spec.Replicas),
				"frontend":    int(*fetchedFrontend.Spec.Replicas),
			}

			// Appeler la nouvelle fonction Collect
			collector.Collect(currentPower, refPower, fetchedScaler.Status.Mode, replicas)
		}

		By("Exporting collected data to CSV")
		err := collector.ExportToCSV("weighting_effectiveness_results.csv")
		Expect(err).NotTo(HaveOccurred())

		By("Cleaning up resources")
		// ...
	}, SpecTimeout(scenarioTimeout))
})