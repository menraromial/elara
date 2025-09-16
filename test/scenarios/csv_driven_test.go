// Package scenarios contains long-running tests that simulate real-world conditions
// to validate the dynamic behavior of the Elara controller.
package scenarios

import (
	//"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	scalingv1alpha1 "elara/api/v1alpha1"

	//appsv1 "k8s.io/api/apps/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PowerSignalPoint represents the power of all nodes at a specific time from the input CSV.
type PowerSignalPoint struct {
	TimeSeconds float64
	NodePowers  []float64
}

type Metadata struct {
	ScenarioName string            `yaml:"scenarioName"`
	Timestamp    time.Time         `yaml:"timestamp"`
	Controller   ControllerConfig  `yaml:"controller"`
	Deployments  DeploymentConfig  `yaml:"deployments"`
}

// ControllerConfig holds the parameters of the ElaraScaler.
type ControllerConfig struct {
	Deadband          string `yaml:"deadband"`
	IncreaseWindow    string `yaml:"increaseWindow"`
	DecreaseWindow    string `yaml:"decreaseWindow"`
	IncreaseTolerance string `yaml:"increaseTolerance"`
	DecreaseTolerance string `yaml:"decreaseTolerance"`
}

// DeploymentConfig holds the structure of the managed applications.
type DeploymentConfig struct {
	Groups      []GroupMetadata      `yaml:"groups"`
	Independent []DeploymentMetadata `yaml:"independent"`
}

// GroupMetadata represents a deployment group.
type GroupMetadata struct {
	Name    string               `yaml:"name"`
	Members []DeploymentMetadata `yaml:"members"`
}

// DeploymentMetadata holds the parameters for a single deployment.
type DeploymentMetadata struct {
	Name        string `yaml:"name"`
	Weight      *int32 `yaml:"weight,omitempty"` // Pointer to allow omitting for independent deployments
	MinReplicas int32  `yaml:"minReplicas"`
	MaxReplicas int32  `yaml:"maxReplicas"`
}
const (
	filePrefix = "Many_Ramp_Power_Test"
	sourceFile = "many_ramp_power.csv"
)



// loadPowerSignalFromCSV reads a CSV file and returns a slice of PowerSignalPoints.
func loadPowerSignalFromCSV(filePath string) ([]PowerSignalPoint, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("could not open csv file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("could not read csv file: %w", err)
	}

	if len(records) < 2 {
		return nil, fmt.Errorf("csv file must have a header and at least one data row")
	}

	var signal []PowerSignalPoint
	// Start from row 1 to skip the header.
	for _, record := range records[1:] {
		timeSec, err := strconv.ParseFloat(record[0], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid TimeSeconds value: %s", record[0])
		}

		var nodePowers []float64
		for _, powerStr := range record[1:] {
			power, err := strconv.ParseFloat(powerStr, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid power value: %s", powerStr)
			}
			nodePowers = append(nodePowers, power)
		}
		signal = append(signal, PowerSignalPoint{TimeSeconds: timeSec, NodePowers: nodePowers})
	}
	return signal, nil
}

var _ = Describe("Scenario: CSV-Driven Multi-Node Test", func() {

	const (
		scenarioTimeout = time.Minute * 10
		tickInterval    = time.Second * 2 // Should match the TimeSeconds interval in the CSV
		namespace       = "default"
		
	)

	It("should scale according to a power signal read from a CSV file", func(ctx SpecContext) {
		By("Loading the power signal from the input CSV")
		_, currentFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue(), "Failed to get current file path")
		currentDir := filepath.Dir(currentFile)
		csvFilePath := filepath.Join(currentDir, "test_data", sourceFile)

		// Affiche le chemin absolu pour le débogage.
		fmt.Printf("Attempting to load CSV from: %s\n", csvFilePath)

		powerSignal, err := loadPowerSignalFromCSV(csvFilePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(powerSignal).NotTo(BeEmpty())

		numNodes := len(powerSignal[0].NodePowers)
		By(fmt.Sprintf("Setting up test environment with %d nodes", numNodes))

		var nodes []*corev1.Node
		for i := 0; i < numNodes; i++ {
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:        fmt.Sprintf("scenario-node-csv-%d", i),
					Annotations: map[string]string{"elara.dev/power-consumption": "0.0"}, // Will be set by the loop
				},
			}
			Expect(k8sClient.Create(ctx, node)).Should(Succeed())
			nodes = append(nodes, node)
		}

        // Create deployments for the new topology
		// Group 1: "backend-services"
		createDeployment(ctx, k8sClient, "auth-service", namespace, 2)
		createDeployment(ctx, k8sClient, "payment-service", namespace, 2)
		// Group 2: "data-processing"
		createDeployment(ctx, k8sClient, "stream-processor", namespace, 1)
		createDeployment(ctx, k8sClient, "batch-worker", namespace, 1)
		// Independent Apps
		createDeployment(ctx, k8sClient, "caching-layer", namespace, 3)
		createDeployment(ctx, k8sClient, "monitoring-agent", namespace, 2)

		deploymentNames := []string{
			"auth-service", "payment-service", "stream-processor", "batch-worker",
			"caching-layer", "monitoring-agent",
		}

		scaler := &scalingv1alpha1.ElaraScaler{
			ObjectMeta: metav1.ObjectMeta{Name: "csv-mixed-scaler"},
			Spec: scalingv1alpha1.ElaraScalerSpec{
				Deadband: resource.MustParse("0.01"),
				StabilizationWindow: scalingv1alpha1.StabilizationWindowSpec{
					Increase:          metav1.Duration{Duration: 4 * time.Second},
					Decrease:          metav1.Duration{Duration: 4 * time.Second},
					IncreaseTolerance: resource.MustParse("0.2"),
					DecreaseTolerance: resource.MustParse("0.2"),
				},
				DeploymentGroups: []scalingv1alpha1.DeploymentGroupSpec{
					{
						Name: "backend-services",
						Members: []scalingv1alpha1.GroupMemberSpec{
							{Weight: 3, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "auth-service", Namespace: namespace, MinReplicas: 2, MaxReplicas: 15}},
							{Weight: 2, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "payment-service", Namespace: namespace, MinReplicas: 2, MaxReplicas: 10}},
						},
					},
					{
						Name: "data-processing",
						Members: []scalingv1alpha1.GroupMemberSpec{
							{Weight: 5, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "stream-processor", Namespace: namespace, MinReplicas: 1, MaxReplicas: 20}},
							{Weight: 1, DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "batch-worker", Namespace: namespace, MinReplicas: 1, MaxReplicas: 20}},
						},
					},
				},
				IndependentDeployments: []scalingv1alpha1.IndependentDeploymentSpec{
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "caching-layer", Namespace: namespace, MinReplicas: 3, MaxReplicas: 12}},
					{DeploymentTargetSpec: scalingv1alpha1.DeploymentTargetSpec{Name: "monitoring-agent", Namespace: namespace, MinReplicas: 2, MaxReplicas: 5}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, scaler)).Should(Succeed())

		By("Exporting scaler metadata")
		exportMetadataAsYAML(scaler)
		exportMetadata(scaler) // Export the configuration

		collector := NewCollector()
		//deploymentNames := []string{"db-service", "api-service", "web-frontend", "job-worker"}

		By("Running the simulation loop driven by the CSV file")
		ticker := time.NewTicker(tickInterval)
		defer ticker.Stop()

		signalIndex := 0
		for range ticker.C {
			if signalIndex >= len(powerSignal) {
				break
			}

			currentSignalPoint := powerSignal[signalIndex]

			// Update all nodes with the power from the current signal point
			totalPower := 0.0
			for i, node := range nodes {
				power := currentSignalPoint.NodePowers[i]
				totalPower += power
				nodeKey := client.ObjectKeyFromObject(node)
				Eventually(ctx, func() error {
					err := k8sClient.Get(ctx, nodeKey, node)
					if err != nil {
						return err
					}
					node.Annotations["elara.dev/power-consumption"] = fmt.Sprintf("%.2f", power)
					return k8sClient.Update(ctx, node)
				}, "5s", "100ms").Should(Succeed())
			}

			// Collect data
			collectAllData(ctx, collector, totalPower, scaler, deploymentNames, namespace)

			signalIndex++
		}

		By("Exporting collected data to CSV")
		err = collector.ExportToCSV(fmt.Sprintf("%s.csv", filePrefix))
		Expect(err).NotTo(HaveOccurred())

		By("Cleaning up resources")
		// Delete the scaler object
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, scaler))).Should(Succeed())

		// Delete all deployments created in this test
		for _, name := range deploymentNames {
			dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, dep))).Should(Succeed())
		}
		
		// Delete all nodes created in this test
		for _, node := range nodes {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, node))).Should(Succeed())
		}
	}, SpecTimeout(scenarioTimeout))
})

// exportMetadata saves the ElaraScaler's configuration to a CSV file.
func exportMetadata(scaler *scalingv1alpha1.ElaraScaler) {
	outputDir := filepath.Join("..", "..", "test_output") // Relative to test execution dir
	os.MkdirAll(outputDir, os.ModePerm)
	filePath := filepath.Join(outputDir, "csv_driven_metadata.csv")

	file, err := os.Create(filePath)
	Expect(err).NotTo(HaveOccurred())
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	writer.Write([]string{"Parameter", "Value"})
	writer.Write([]string{"Deadband", scaler.Spec.Deadband.String()})
	writer.Write([]string{"IncreaseWindow", scaler.Spec.StabilizationWindow.Increase.Duration.String()})
	writer.Write([]string{"DecreaseWindow", scaler.Spec.StabilizationWindow.Decrease.Duration.String()})
	writer.Write([]string{"IncreaseTolerance", scaler.Spec.StabilizationWindow.IncreaseTolerance.String()})
	writer.Write([]string{"DecreaseTolerance", scaler.Spec.StabilizationWindow.DecreaseTolerance.String()})

	for _, group := range scaler.Spec.DeploymentGroups {
		for _, member := range group.Members {
			prefix := fmt.Sprintf("%s.%s", group.Name, member.Name)
			writer.Write([]string{prefix + ".Weight", strconv.Itoa(int(member.Weight))})
			writer.Write([]string{prefix + ".MinReplicas", strconv.Itoa(int(member.MinReplicas))})
			writer.Write([]string{prefix + ".MaxReplicas", strconv.Itoa(int(member.MaxReplicas))})
		}
	}
	fmt.Printf("Successfully exported metadata to %s\n", filePath)
}

// exportMetadataAsYAML saves the ElaraScaler's configuration to a well-structured YAML file.
func exportMetadataAsYAML(scaler *scalingv1alpha1.ElaraScaler) {
	outputDir := filepath.Join("..", "..", "test_output") // Relative to test execution dir
	os.MkdirAll(outputDir, os.ModePerm)
	//scenarioName := "Solar_Power_results"
	// Example filename: csv_driven_metadata.yaml
	filename := fmt.Sprintf("%s_metadata.yaml", filePrefix)
	filePath := filepath.Join(outputDir, filename)

	// Build the structured metadata object from the scaler spec
	meta := Metadata{
		ScenarioName: filePrefix,
		Timestamp:    time.Now(),
		Controller: ControllerConfig{
			Deadband:          scaler.Spec.Deadband.String(),
			IncreaseWindow:    scaler.Spec.StabilizationWindow.Increase.Duration.String(),
			DecreaseWindow:    scaler.Spec.StabilizationWindow.Decrease.Duration.String(),
			IncreaseTolerance: scaler.Spec.StabilizationWindow.IncreaseTolerance.String(),
			DecreaseTolerance: scaler.Spec.StabilizationWindow.DecreaseTolerance.String(),
		},
		Deployments: DeploymentConfig{
			Groups:      []GroupMetadata{},
			Independent: []DeploymentMetadata{},
		},
	}

	// Populate groups
	for _, groupSpec := range scaler.Spec.DeploymentGroups {
		groupMeta := GroupMetadata{Name: groupSpec.Name, Members: []DeploymentMetadata{}}
		for _, memberSpec := range groupSpec.Members {
			weight := memberSpec.Weight // Make a copy
			groupMeta.Members = append(groupMeta.Members, DeploymentMetadata{
				Name:        memberSpec.Name,
				Weight:      &weight,
				MinReplicas: memberSpec.MinReplicas,
				MaxReplicas: memberSpec.MaxReplicas,
			})
		}
		meta.Deployments.Groups = append(meta.Deployments.Groups, groupMeta)
	}

	// Populate independent deployments
	for _, indepSpec := range scaler.Spec.IndependentDeployments {
		meta.Deployments.Independent = append(meta.Deployments.Independent, DeploymentMetadata{
			Name:        indepSpec.Name,
			MinReplicas: indepSpec.MinReplicas,
			MaxReplicas: indepSpec.MaxReplicas,
		})
	}

	// Marshal the struct into YAML bytes
	yamlData, err := yaml.Marshal(&meta)
	Expect(err).NotTo(HaveOccurred())

	// Write the YAML data to the file
	err = os.WriteFile(filePath, yamlData, 0644)
	Expect(err).NotTo(HaveOccurred())

	fmt.Printf("Successfully exported metadata to %s\n", filePath)
}