package controllers

import (
	"context"
	"fmt"
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	//"k8s.io/apimachinery/pkg/api/errors"
	//"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	greenopsv1 "elara/api/v1" // IMPORTANT: Use your module name
)

// Data structure to hold the metrics collected at each step of the ramp.
type RampDataPoint struct {
	Timestamp      time.Time
	PowerLevel     float64
	TargetReplicas int32
	ActualReplicas int32
	Error          int32
}

var _ = Describe("ElaraPolicy Controller: Ramp Test", func() {

	const (
		// Use a fixed namespace for testing, as we won't be deleting it.
		// "default" is always available in envtest.
		RampTestNamespace = "default" 
		RampPolicyName    = "ramp-test-policy"
		NodeName          = "ramp-test-node"
		// Experiment parameters
		NumRampSteps = 10                  // Number of steps for ramp down and up
		StepDelay    = 5 * time.Second   // Time to wait between power changes
		rampTimeout  = 60 * time.Second    // General timeout for assertions (not cleanup hangs)
		rampInterval = 250 * time.Millisecond
	)

	ctx := context.Background()

	// BeforeEach: We no longer create a unique namespace per test.
	// We rely on 'default' or a fixed one that is assumed to exist.
	BeforeEach(func() {
		By(fmt.Sprintf("Using fixed namespace '%s' for ramp test (not explicitly created/deleted)", RampTestNamespace))
	})

	// AfterEach: This is the definitive cleanup logic that will prevent hangs.
	AfterEach(func() {
		By("Cleaning up ramp test resources (excluding namespace itself)")

		// Step 1: Delete the ElaraPolicy first to stop the controller from reconciling.
		policy := &greenopsv1.ElaraPolicy{ObjectMeta: metav1.ObjectMeta{Name: RampPolicyName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).Should(Succeed())

		// Step 2: Delete the test node.
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())

		// Step 3: Explicitly delete all deployments created by this test in the fixed namespace.
		By(fmt.Sprintf("Explicitly deleting all mock deployments in namespace '%s'", RampTestNamespace))
		deploymentList := &appsv1.DeploymentList{}
		Expect(k8sClient.List(ctx, deploymentList, client.InNamespace(RampTestNamespace))).Should(Succeed())
		for _, dep := range deploymentList.Items {
			// Ensure we only delete deployments *created by this test* if using a shared namespace.
			// In this case, our naming convention 'app-xxx' makes them unique.
			if dep.ObjectMeta.Name != "" { // Simple check to avoid deleting random things
			    Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &dep))).Should(Succeed())
			}
		}
		
		// Step 4: Wait for all deployments to be gone. This is critical to prevent resource leaks.
		By(fmt.Sprintf("Waiting for all deployments to be deleted from namespace '%s'", RampTestNamespace))
		Eventually(func() (int, error) {
			err := k8sClient.List(ctx, deploymentList, client.InNamespace(RampTestNamespace))
			if err != nil { return -1, err }
			return len(deploymentList.Items), nil
		}, rampTimeout, rampInterval).Should(BeZero(), "All deployments should be deleted from the fixed namespace")
		
		// IMPORTANT: No namespace deletion here, as per your requirement.
	})

	It("should track a gradual power signal with low replication error", func() {
		// --- SETUP ---
		managedDeployments := generateManagedDeployments(RampTestNamespace, 20) // Use a smaller set for a faster test
		for _, md := range managedDeployments {
			dep := createMockDeployment(md.Namespace, md.Name, md.MaxReplicas)
			Expect(k8sClient.Create(ctx, dep)).Should(Succeed())
		}
		
		node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: NodeName, Labels: map[string]string{optimalPowerLabel: "1000", currentPowerLabel: "1000"}}}
		Expect(k8sClient.Create(ctx, node)).Should(Succeed())
		// Defer is not needed here as Node is explicitly deleted in AfterEach
		// defer k8sClient.Delete(ctx, node) 
		
		policy := &greenopsv1.ElaraPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: RampPolicyName},
			Spec:       greenopsv1.ElaraPolicySpec{Deployments: managedDeployments},
		}
		Expect(k8sClient.Create(ctx, policy)).Should(Succeed())

		// Ensure system starts at a stable, optimal state.
		By("Ensuring initial state is stable at max replicas")
		expectedInitialReplicas := make(map[string]int32)
		for _, md := range managedDeployments { expectedInitialReplicas[md.Name] = md.MaxReplicas }
		assertAllDeploymentsConverged(ctx, RampTestNamespace, expectedInitialReplicas)

		// --- RAMP DOWN & DATA COLLECTION ---
		By("Beginning ramp down from 100% to 40% power")
		var collectedData []RampDataPoint
		scaler := &DeclarativeScaler{Deployments: managedDeployments}

		optimalPower := 1000.0
		minPower := 400.0
		powerStep := (optimalPower - minPower) / float64(NumRampSteps)

		for i := 1; i <= NumRampSteps; i++ {
			currentPower := optimalPower - (float64(i) * powerStep)
			
			By(fmt.Sprintf("Ramp Down Step %d/%d: Setting power to %.2f", i, NumRampSteps, currentPower))
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
			node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			time.Sleep(StepDelay) // Wait for the system to react

			// Collect data point
			dataPoint := collectDataPoint(ctx, scaler, RampTestNamespace, currentPower, optimalPower)
			collectedData = append(collectedData, dataPoint)
		}

		// --- RAMP UP & DATA COLLECTION ---
		By("Beginning ramp up from 40% to 100% power")
		for i := 1; i <= NumRampSteps; i++ {
			currentPower := minPower + (float64(i) * powerStep)
			
			By(fmt.Sprintf("Ramp Up Step %d/%d: Setting power to %.2f", i, NumRampSteps, currentPower))
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: NodeName}, node)).Should(Succeed())
			node.Labels[currentPowerLabel] = fmt.Sprintf("%.2f", currentPower)
			Expect(k8sClient.Update(ctx, node)).Should(Succeed())

			time.Sleep(StepDelay)

			dataPoint := collectDataPoint(ctx, scaler, RampTestNamespace, currentPower, optimalPower)
			collectedData = append(collectedData, dataPoint)
		}

		// --- FINAL VALIDATION & REPORTING ---
		By("Asserting final state has returned to max replicas")
		assertAllDeploymentsConverged(ctx, RampTestNamespace, expectedInitialReplicas)

		By("Reporting collected metrics")
		var totalError int64
		var maxError int32
		fmt.Println("\n--- METRICS: RAMP TEST RESULTS ---")
		fmt.Println("Step,PowerLevel,TargetReplicas,ActualReplicas,ReplicationError")
		for i, data := range collectedData {
			fmt.Printf("%d,%.2f,%d,%d,%d\n", i+1, data.PowerLevel, data.TargetReplicas, data.ActualReplicas, data.Error)
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

		// A final assertion for the test itself.
		Expect(avgError).To(BeNumerically("<", 5), "Average replication error should be low")
	})
})

// collectDataPoint is a helper to measure the system's state at a point in time.
func collectDataPoint(ctx context.Context, scaler *DeclarativeScaler, namespace string, currentPower, optimalPower float64) RampDataPoint {
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
		PowerLevel:     currentPower,
		TargetReplicas: totalTargetReplicas,
		ActualReplicas: totalActualReplicas,
		Error:          errorVal,
	}
}

// // createMockDeployment is a helper function to create a basic Deployment object for tests.
// func createMockDeployment(namespace, name string, replicas int32) *appsv1.Deployment {
// 	return &appsv1.Deployment{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name:      name,
// 			Namespace: namespace,
// 		},
// 		Spec: appsv1.DeploymentSpec{
// 			Replicas: &replicas,
// 			Selector: &metav1.LabelSelector{
// 				MatchLabels: map[string]string{"app": name},
// 			},
// 			Template: corev1.PodTemplateSpec{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Labels: map[string]string{"app": name},
// 				},
// 				Spec: corev1.PodSpec{
// 					Containers: []corev1.Container{{
// 						Name:  "main",
// 						Image: "nginx",
// 					}},
// 				},
// 			},
// 		},
// 	}
// }

// // generateManagedDeployments creates a diverse list of deployment specs for the policy.
// func generateManagedDeployments(namespace string, count int) []greenopsv1.ManagedDeployment {
// 	deployments := make([]greenopsv1.ManagedDeployment, count)
// 	for i := 0; i < count; i++ {
// 		name := fmt.Sprintf("app-%03d", i)
// 		group := fmt.Sprintf("group-%d", i%5)
		
// 		deployments[i] = greenopsv1.ManagedDeployment{
// 			Name:        name,
// 			Namespace:   namespace,
// 			MinReplicas: 1,
// 			MaxReplicas: int32(5 + (i % 15)),
// 			Weight:      resource.MustParse("1.0"),
// 			Group:       group,
// 		}
// 	}
// 	// Make some deployments independent
// 	for i := 0; i < count/10; i++ { // 10% are independent
// 		deployments[i].Group = ""
// 	}
// 	return deployments
// }

// // assertAllDeploymentsConverged is an efficient assertion helper.
// func assertAllDeploymentsConverged(ctx context.Context, namespace string, expectedReplicas map[string]int32) {
// 	Eventually(func(g Gomega) {
// 		deploymentList := &appsv1.DeploymentList{}
// 		err := k8sClient.List(ctx, deploymentList, client.InNamespace(namespace))
// 		g.Expect(err).NotTo(HaveOccurred(), "Should be able to list deployments")
		
// 		currentReplicas := make(map[string]int32)
// 		for _, dep := range deploymentList.Items {
// 			if dep.Spec.Replicas != nil {
// 				currentReplicas[dep.Name] = *dep.Spec.Replicas
// 			} else {
// 				currentReplicas[dep.Name] = 0 
// 			}
// 		}

// 		g.Expect(currentReplicas).To(Equal(expectedReplicas), "All deployments should converge to their target replica counts")

// 	}, rampTimeout, rampInterval).Should(Succeed())
// }