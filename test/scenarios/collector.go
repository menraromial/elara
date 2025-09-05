// Package scenarios contains long-running tests that simulate real-world conditions
// to validate the dynamic behavior of the Elara controller.
package scenarios

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// DataPoint holds a snapshot of the system's state at a specific moment in time.
// It is used to record the evolution of the system during a scenario test.
type DataPoint struct {
	// Timestamp is the wall-clock time when the data point was captured.
	Timestamp time.Time
	// TimeSeconds is the elapsed time in seconds since the beginning of the simulation.
	TimeSeconds float64
	// PowerSignal is the external power value (in Watts) provided to the controller at this instant.
	PowerSignal float64
	// ReferencePower is the controller's internal P_ref value at this instant.
	ReferencePower float64
	// ControllerMode is the current state of the controller's state machine (e.g., "Stable", "PendingIncrease").
	ControllerMode string
	// TotalReplicas is the sum of replicas across all monitored deployments.
	TotalReplicas int
	// DeploymentReplicas maps each deployment's name to its replica count at this instant.
	// Using a map makes the collector flexible and reusable across different scenarios.
	DeploymentReplicas map[string]int
}

// Collector manages the collection of DataPoints during a simulation and handles their export to a CSV file.
type Collector struct {
	// DataPoints stores the sequence of snapshots recorded during the test.
	DataPoints []DataPoint
	// startTime marks the beginning of the simulation, used to calculate elapsed time.
	startTime time.Time
}

// NewCollector creates and initializes a new data collector.
// It sets the start time to the moment of its creation.
func NewCollector() *Collector {
	return &Collector{
		startTime: time.Now(),
	}
}

// Collect captures the current state of the system and appends it as a new DataPoint.
// It accepts a map of replica counts, making it adaptable to scenarios with any number of deployments.
func (c *Collector) Collect(power, refPower float64, mode string, replicas map[string]int) {
	now := time.Now()
	total := 0
	// Calculate the total number of replicas from the provided map.
	for _, r := range replicas {
		total += r
	}

	dp := DataPoint{
		Timestamp:          now,
		TimeSeconds:        now.Sub(c.startTime).Seconds(),
		PowerSignal:        power,
		ReferencePower:     refPower,
		ControllerMode:     mode,
		TotalReplicas:      total,
		DeploymentReplicas: replicas,
	}
	c.DataPoints = append(c.DataPoints, dp)
}

// ExportToCSV writes all collected DataPoints to a specified CSV file.
// The file is saved in a predefined output directory ('test_output' at the project root).
// The CSV header is generated dynamically based on the names of the deployments collected.
func (c *Collector) ExportToCSV(filename string) error {
	// Ensure there is data to write.
	if len(c.DataPoints) == 0 {
		return fmt.Errorf("no data points collected to export")
	}

	// Define the output directory relative to the project root.
	// The test command is run from the root, so this path is reliable.
	outputDir := filepath.Join("..", "..", "test_output") 
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output directory '%s': %w", outputDir, err)
	}

	filePath := filepath.Join(outputDir, filename)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create CSV file '%s': %w", filePath, err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// --- Dynamically generate the CSV header ---
	// Start with the fixed columns.
	header := []string{
		"Timestamp", "TimeSeconds", "PowerSignal", "ReferencePower", "ControllerMode", "TotalReplicas",
	}

	// Extract deployment names from the first data point to create the dynamic part of the header.
	firstDataPoint := c.DataPoints[0]
	depNames := make([]string, 0, len(firstDataPoint.DeploymentReplicas))
	for name := range firstDataPoint.DeploymentReplicas {
		depNames = append(depNames, name)
	}
	// Sort the names alphabetically to ensure a consistent column order in the CSV file.
	sort.Strings(depNames)

	// Append the sorted deployment names to the header.
	header = append(header, depNames...)

	// Write the complete header to the file.
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("failed to write header to CSV: %w", err)
	}

	// --- Write all data rows ---
	for _, dp := range c.DataPoints {
		// Start each record with the fixed data fields.
		record := []string{
			dp.Timestamp.Format(time.RFC3339),
			strconv.FormatFloat(dp.TimeSeconds, 'f', 2, 64),
			strconv.FormatFloat(dp.PowerSignal, 'f', 2, 64),
			strconv.FormatFloat(dp.ReferencePower, 'f', 2, 64),
			dp.ControllerMode,
			strconv.Itoa(dp.TotalReplicas),
		}

		// Append the replica counts for each deployment, in the same order as the header.
		for _, name := range depNames {
			replicaCount, ok := dp.DeploymentReplicas[name]
			if !ok {
				// This case should ideally not happen if data is collected consistently.
				// Add an empty string as a fallback.
				record = append(record, "")
			} else {
				record = append(record, strconv.Itoa(replicaCount))
			}
		}

		// Write the complete record to the file.
		if err := writer.Write(record); err != nil {
			return fmt.Errorf("failed to write record to CSV: %w", err)
		}
	}

	fmt.Printf("Successfully exported data to %s\n", filePath)
	return nil
}