/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	// ... other imports
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"

	//"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	scalingv1alpha1 "elara/api/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	// Annotation for power consumption on Node objects
	powerAnnotation = "elara.dev/power-consumption"
	// Master/control-plane node roles to exclude from power calculation
	masterRoleLabel       = "node-role.kubernetes.io/master"
	controlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
)

// ElaraScalerReconciler reconciles a ElaraScaler object
type ElaraScalerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// deploymentInfo holds the live state and configuration for a single target deployment.
type deploymentInfo struct {
	// from the ElaraScaler spec
	Name      string
	Namespace string
	Min       int32
	Max       int32
	Weight    int32 // Will be used later for groups

	// live data from the cluster
	CurrentReplicas int32
	Ref             *appsv1.Deployment // A reference to the actual deployment object
}

// scalingEntities represents the complete set of deployments to be scaled, organized
// into independent units and groups.
type scalingEntities struct {
	Deployments map[types.NamespacedName]*deploymentInfo
	Independent []types.NamespacedName
	Groups      map[string][]types.NamespacedName
}

//+kubebuilder:rbac:groups=scaling.elara.dev,resources=elarascalers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=scaling.elara.dev,resources=elarascalers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=scaling.elara.dev,resources=elarascalers/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *ElaraScalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var scaler scalingv1alpha1.ElaraScaler
	if err := r.Get(ctx, req.NamespacedName, &scaler); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	currentPower, err := r.calculateClusterPower(ctx, logger)
	if err != nil {
		logger.Error(err, "Failed to calculate cluster power")
		return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
	}
	logger.Info("Successfully calculated cluster power", "powerWatts", currentPower.AsApproximateFloat64())

	if scaler.Status.Mode == "" {
		return r.initializeStatus(ctx, logger, scaler, *currentPower)
	}

	switch scaler.Status.Mode {
	case "Stable":
		return r.handleStableState(ctx, logger, scaler, *currentPower)
	case "PendingIncrease":
		return r.handlePendingState(ctx, logger, scaler, *currentPower, true)
	case "PendingDecrease":
		return r.handlePendingState(ctx, logger, scaler, *currentPower, false)
	}

	logger.Info("Reconciliation finished with unhandled state", "state", scaler.Status.Mode)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// calculateClusterPower sums the power from all worker nodes.
func (r *ElaraScalerReconciler) calculateClusterPower(ctx context.Context, logger logr.Logger) (*resource.Quantity, error) {
	nodeList := &corev1.NodeList{}
	if err := r.List(ctx, nodeList); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	totalPower := resource.NewQuantity(0, resource.DecimalSI)

	for _, node := range nodeList.Items {
		// Skip master/control-plane nodes
		if _, ok := node.Labels[masterRoleLabel]; ok {
			continue
		}
		if _, ok := node.Labels[controlPlaneRoleLabel]; ok {
			continue
		}

		// Get power from annotation
		powerStr, ok := node.Annotations[powerAnnotation]
		if !ok {
			return nil, fmt.Errorf("node %s is missing power annotation '%s'", node.Name, powerAnnotation)
		}

		powerVal, err := strconv.ParseFloat(powerStr, 64)
		if err != nil {
			return nil, fmt.Errorf("could not parse power value '%s' for node %s: %w", powerStr, node.Name, err)
		}

		totalPower.Add(*resource.NewMilliQuantity(int64(powerVal*1000), resource.DecimalSI))
	}

	return totalPower, nil
}

// calculateRelativeChange computes (current - reference) / reference.
// It returns 0 if the reference is zero to avoid division by zero.
func calculateRelativeChange(current, reference resource.Quantity) float64 {
	refFloat := reference.AsApproximateFloat64()
	if refFloat == 0 {
		// If reference is 0, any change is infinite.
		// For our purpose, if current is also 0, change is 0. Otherwise, it's a significant change.
		if current.IsZero() {
			return 0.0
		}
		return 999.0 // A large number to signify a major change
	}

	currentFloat := current.AsApproximateFloat64()
	return (currentFloat - refFloat) / refFloat
}

// Replace the placeholder handleStableState function with this complete version.

// handleStableState implements the logic for when the controller is in the Stable state.
// It checks if the power change exceeds the deadband and transitions to a Pending state if so.
func (r *ElaraScalerReconciler) handleStableState(ctx context.Context, logger logr.Logger, scaler scalingv1alpha1.ElaraScaler, currentPower resource.Quantity) (ctrl.Result, error) {
	logger.Info("In Stable state, checking for significant power change", "referencePower", scaler.Status.ReferencePower.String(), "currentPower", currentPower.String())

	// Calculate relative power change: ΔP_rel = (P(t) - P_ref) / P_ref
	relativeChange := calculateRelativeChange(currentPower, *scaler.Status.ReferencePower)
	deadband := scaler.Spec.Deadband.AsApproximateFloat64() // Corresponds to δ

	// Case 1: Change is insignificant. Do nothing.
	if abs(relativeChange) < deadband {
		logger.Info("Power change is within the deadband, no action needed.", "relativeChange", fmt.Sprintf("%.4f", relativeChange), "deadband", deadband)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Case 2: Significant increase. Transition to PendingIncrease.
	if relativeChange >= deadband {
		logger.Info("Significant power increase detected, transitioning to PendingIncrease.", "relativeChange", fmt.Sprintf("%.4f", relativeChange))
		scaler.Status.Mode = "PendingIncrease"
		scaler.Status.TriggerTime = &metav1.Time{Time: time.Now()}
		tmpPower := currentPower.DeepCopy()
		scaler.Status.TriggerPower = &tmpPower // P_trigger ← P(t)
	} else { // Case 3: Significant decrease. Transition to PendingDecrease.
		logger.Info("Significant power decrease detected, transitioning to PendingDecrease.", "relativeChange", fmt.Sprintf("%.4f", relativeChange))
		scaler.Status.Mode = "PendingDecrease"
		scaler.Status.TriggerTime = &metav1.Time{Time: time.Now()}
		tmpPower := currentPower.DeepCopy()
		scaler.Status.TriggerPower = &tmpPower // P_trigger ← P(t)
	}

	// Update the status on the API server to persist the state change.
	if err := r.Status().Update(ctx, &scaler); err != nil {
		logger.Error(err, "Failed to update status for state transition")
		return ctrl.Result{}, err
	}

	logger.Info("Successfully transitioned to new state", "newState", scaler.Status.Mode)
	// We requeue immediately to start processing in the new state.
	return ctrl.Result{Requeue: true}, nil
}

// abs is a simple helper for absolute value of a float64.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// handlePendingState implements the logic for PENDING_INCREASE and PENDING_DECREASE states.
func (r *ElaraScalerReconciler) handlePendingState(ctx context.Context, logger logr.Logger, scaler scalingv1alpha1.ElaraScaler, currentPower resource.Quantity, isIncrease bool) (ctrl.Result, error) {
	var windowDuration time.Duration
	var tolerance float64
	if isIncrease {
		windowDuration = scaler.Spec.StabilizationWindow.Increase.Duration                   // W_inc
		tolerance = scaler.Spec.StabilizationWindow.IncreaseTolerance.AsApproximateFloat64() // ε_inc
	} else {
		windowDuration = scaler.Spec.StabilizationWindow.Decrease.Duration                   // W_dec
		tolerance = scaler.Spec.StabilizationWindow.DecreaseTolerance.AsApproximateFloat64() // ε_dec
	}

	// The time when the pending state was triggered.
	triggerTime := scaler.Status.TriggerTime.Time

	// Case 1: The stabilization window has not yet passed.
	if time.Since(triggerTime) < windowDuration {
		remaining := windowDuration - time.Since(triggerTime)
		logger.Info("Waiting for stabilization window to pass", "state", scaler.Status.Mode, "remaining", remaining.Round(time.Second))
		// Requeue exactly after the remaining time for efficiency.
		return ctrl.Result{RequeueAfter: remaining}, nil
	}

	// Case 2: The window has passed. Now we check for stability.
	logger.Info("Stabilization window has passed. Checking for signal stability.", "state", scaler.Status.Mode)

	// Check stability: |P(t) - P_trigger| / P_trigger <= ε
	stabilityCheck := calculateRelativeChange(currentPower, *scaler.Status.TriggerPower)

	if abs(stabilityCheck) <= tolerance {
		// Signal is stable! It's time to trigger the scaling logic.
		logger.Info("Signal is stable. Proceeding to Phase 2: Scaling Calculation.", "stabilityChange", fmt.Sprintf("%.4f", stabilityCheck), "tolerance", tolerance)

		// Call the (currently placeholder) scaling function.
		return r.performScaling(ctx, logger, scaler, currentPower)

	} else {
		// Signal was not stable. The potential scaling action is cancelled.
		// Reset the state machine back to Stable.
		logger.Info("Signal was not stable during the window. Cancelling action and resetting to Stable.", "stabilityChange", fmt.Sprintf("%.4f", stabilityCheck), "tolerance", tolerance)

		scaler.Status.Mode = "Stable"
		powerCopy := currentPower.DeepCopy()
		scaler.Status.ReferencePower = &powerCopy // P_ref ← P(t)
		scaler.Status.TriggerTime = nil
		scaler.Status.TriggerPower = nil

		if err := r.Status().Update(ctx, &scaler); err != nil {
			logger.Error(err, "Failed to update status after unstable signal")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
}

// performScaling implements Phase 2: Calculation and Scaling.
func (r *ElaraScalerReconciler) performScaling(ctx context.Context, logger logr.Logger, scaler scalingv1alpha1.ElaraScaler, currentPower resource.Quantity) (ctrl.Result, error) {
	logger.Info("Phase 2: Starting scaling calculation.")

	// 1. Fetch all target deployments and their current state.
	entities, err := r.fetchAllTargetDeployments(ctx, logger, scaler)
	if err != nil {
		logger.Error(err, "Could not fetch target deployments, aborting scaling cycle.")
		return r.resetToStable(ctx, logger, scaler, currentPower)
	}

	if len(entities.Deployments) == 0 {
		logger.Info("No target deployments found or available. Nothing to scale.")
		return r.resetToStable(ctx, logger, scaler, currentPower)
	}

	// 2. Calculate total current replicas (C_total)
	var totalCurrentReplicas int32
	for _, depInfo := range entities.Deployments {
		totalCurrentReplicas += depInfo.CurrentReplicas
	}
	logger.Info("Calculated total replicas for targeted deployments", "C_total", totalCurrentReplicas)

	// 3. Calculate the total desired change in replicas (Δ_total)
	powerChange := calculateRelativeChange(currentPower, *scaler.Status.ReferencePower)
	deltaTotalFloat := float64(totalCurrentReplicas) * powerChange
	deltaTotal := int(math.Round(deltaTotalFloat))
	logger.Info("Calculated total replica change target", "powerChange", fmt.Sprintf("%.4f", powerChange), "deltaTotalFloat", deltaTotalFloat, "deltaTotal", deltaTotal)

	if deltaTotal == 0 {
		logger.Info("Calculated replica change is zero. No scaling action needed.")
		return r.resetToStable(ctx, logger, scaler, currentPower)
	}

	// 4. Distribute Δ_total among entities
	entityTargets := make(map[string]int)
	proportions := make(map[string]float64)
	isScaleDown := deltaTotal < 0
	magnitude := int(math.Abs(float64(deltaTotal)))

	if isScaleDown {
		logger.Info("Distribution mode: Scale-Down (based on current usage)")
		var totalConsumption int32
		for _, depInfo := range entities.Deployments {
			totalConsumption += depInfo.CurrentReplicas
		}
		if totalConsumption == 0 {
			logger.Info("Total consumption is zero, cannot distribute scale-down. Aborting.")
			return r.resetToStable(ctx, logger, scaler, currentPower)
		}
		for _, key := range entities.Independent {
			proportions["independent/"+key.String()] = float64(entities.Deployments[key].CurrentReplicas) / float64(totalConsumption)
		}
		for groupName, members := range entities.Groups {
			var groupConsumption int32
			for _, memberKey := range members {
				groupConsumption += entities.Deployments[memberKey].CurrentReplicas
			}
			proportions["group/"+groupName] = float64(groupConsumption) / float64(totalConsumption)
		}
	} else {
		logger.Info("Distribution mode: Scale-Up (based on remaining capacity)")
		var totalCapacity int32
		for _, depInfo := range entities.Deployments {
			totalCapacity += (depInfo.Max - depInfo.CurrentReplicas)
		}
		if totalCapacity == 0 {
			logger.Info("No remaining capacity in any target, cannot scale up. Aborting.")
			return r.resetToStable(ctx, logger, scaler, currentPower)
		}
		for _, key := range entities.Independent {
			depInfo := entities.Deployments[key]
			proportions["independent/"+key.String()] = float64(depInfo.Max-depInfo.CurrentReplicas) / float64(totalCapacity)
		}
		for groupName, members := range entities.Groups {
			var groupCapacity int32
			for _, memberKey := range members {
				depInfo := entities.Deployments[memberKey]
				groupCapacity += (depInfo.Max - depInfo.CurrentReplicas)
			}
			proportions["group/"+groupName] = float64(groupCapacity) / float64(totalCapacity)
		}
	}

	entityTargets = largestRemainderMethod(proportions, magnitude)
	sign := 1
	if isScaleDown {
		sign = -1
	}
	for name, val := range entityTargets {
		entityTargets[name] = val * sign
	}
	logger.Info("Distributed scaling targets to entities", "targets", entityTargets)

	// 5. Distribute group targets among their member deployments
	deploymentChanges := make(map[types.NamespacedName]int)
	for entityName, change := range entityTargets {
		if strings.HasPrefix(entityName, "independent/") {
			keyStr := strings.TrimPrefix(entityName, "independent/")
			parts := strings.Split(keyStr, "/")
			key := types.NamespacedName{Namespace: parts[0], Name: parts[1]}
			deploymentChanges[key] = change
		} else if strings.HasPrefix(entityName, "group/") {
			groupName := strings.TrimPrefix(entityName, "group/")
			members := entities.Groups[groupName]
			groupMagnitude := int(math.Abs(float64(change)))
			var totalWeight int32
			for _, memberKey := range members {
				totalWeight += entities.Deployments[memberKey].Weight
			}
			if totalWeight == 0 {
				continue
			}
			groupProportions := make(map[string]float64)
			for _, memberKey := range members {
				groupProportions[memberKey.String()] = float64(entities.Deployments[memberKey].Weight) / float64(totalWeight)
			}
			distributedMagnitudes := largestRemainderMethod(groupProportions, groupMagnitude)
			groupSign := 1
			if change < 0 {
				groupSign = -1
			}
			for keyStr, val := range distributedMagnitudes {
				parts := strings.Split(keyStr, "/")
				key := types.NamespacedName{Namespace: parts[0], Name: parts[1]}
				deploymentChanges[key] = val * groupSign
			}
		}
	}
	logger.Info("Calculated desired changes for each deployment", "deploymentChanges", formatDeploymentChangesForLog(deploymentChanges))

	// 6. Apply constraints and redistribute
	finalChanges := r.applyConstraintsAndRedistribute(logger, entities, deploymentChanges)

	logger.Info("Calculated final changes after applying constraints and redistribution", "finalChanges", formatDeploymentChangesForLog(finalChanges))

	// Step 7: Apply the final scaling decisions (Patch Deployments)
	if scaler.Spec.DryRun {
		logger.Info("[DryRun] Skipping actual scaling of deployments.", "intendedChanges", formatDeploymentChangesForLog(finalChanges))
	} else {
		logger.Info("Applying scaling changes to deployments.")
		// We'll track if any scaling operation actually happened.
		scaledSomething := false
		for key, change := range finalChanges {
			if change == 0 {
				continue // No change needed for this deployment
			}

			deployment := entities.Deployments[key].Ref
			originalReplicas := *deployment.Spec.Replicas
			newReplicas := originalReplicas + int32(change)

			// Patch the deployment with the new replica count.
			// Using a patch is generally better than Update as it's less prone to conflicts.
			patch := client.MergeFrom(deployment.DeepCopy())
			deployment.Spec.Replicas = &newReplicas

			if err := r.Patch(ctx, deployment, patch); err != nil {
				logger.Error(err, "Failed to patch deployment", "deployment", key.String())
				// We continue to the next deployment even if one fails.
				continue
			}

			logger.Info("Successfully scaled deployment", "deployment", key.String(), "from", originalReplicas, "to", newReplicas)
			scaledSomething = true
		}

		// If we successfully scaled at least one deployment, update the LastScaleTime.
		if scaledSomething {
			scaler.Status.LastScaleTime = &metav1.Time{Time: time.Now()}
		}
	}

	logger.Info("Scaling application logic is not yet implemented. Resetting state for now.")

	// 8. After action, reset the state machine to Stable with the new reference power.
	return r.resetToStable(ctx, logger, scaler, currentPower)
}

// resetToStable is a helper to centralize the logic for returning to a stable state.
func (r *ElaraScalerReconciler) resetToStable(ctx context.Context, logger logr.Logger, scaler scalingv1alpha1.ElaraScaler, newReferencePower resource.Quantity) (ctrl.Result, error) {
	scaler.Status.Mode = "Stable"
	powerCopy := newReferencePower.DeepCopy()
	scaler.Status.ReferencePower = &powerCopy
	scaler.Status.TriggerTime = nil
	scaler.Status.TriggerPower = nil

	// We'll set LastScaleTime once we actually perform a scale.
	// scaler.Status.LastScaleTime = &metav1.Time{Time: time.Now()}

	if err := r.Status().Update(ctx, &scaler); err != nil {
		logger.Error(err, "Failed to update status to reset to Stable")
		return ctrl.Result{}, err
	}
	logger.Info("Controller state has been reset to Stable.", "newReferencePower", newReferencePower.String())
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// initializeStatus sets the initial state for a new ElaraScaler.
func (r *ElaraScalerReconciler) initializeStatus(ctx context.Context, logger logr.Logger, scaler scalingv1alpha1.ElaraScaler, currentPower resource.Quantity) (ctrl.Result, error) {
	logger.Info("Initializing status for new ElaraScaler")
	scaler.Status.Mode = "Stable"
	scaler.Status.ReferencePower = &currentPower
	// TODO: Add a "Ready" condition
	if err := r.Status().Update(ctx, &scaler); err != nil {
		logger.Error(err, "Failed to initialize ElaraScaler status")
		return ctrl.Result{}, err
	}
	return ctrl.Result{Requeue: true}, nil // Requeue immediately to process with the new status
}

// SetupWithManager sets up the controller with the Manager.
func (r *ElaraScalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&scalingv1alpha1.ElaraScaler{}).
		// Watch for changes on Node objects, because power annotations might change.
		// Any change to ANY node will trigger a reconcile for ALL ElaraScaler objects.
		Watches(
			&corev1.Node{}, // Corrected: Pass the type directly
			handler.EnqueueRequestsFromMapFunc(r.findAllElaraScalers),
		).
		// You might also want to watch target Deployments in the future.
		// This ensures that if a Deployment is manually scaled, the controller
		// will notice and can re-evaluate. This is more advanced and can be added later.
		Complete(r)
}

// findAllElaraScalers is a MapFunc that triggers a reconcile for all ElaraScaler
// objects when a Node changes. This is because any node change can affect the
// total cluster power, which is a global input to all policies.
// The function signature is now corrected to include context.Context.
func (r *ElaraScalerReconciler) findAllElaraScalers(ctx context.Context, node client.Object) []reconcile.Request {
	scalerList := &scalingv1alpha1.ElaraScalerList{}
	// Use the provided context for the API call.
	if err := r.List(ctx, scalerList); err != nil {
		// We can't easily log here as we don't have a logger instance.
		// Returning an empty slice is the safest fallback. The controller will eventually retry.
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(scalerList.Items))
	for i, item := range scalerList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: item.GetName(),
				// Namespace is empty for cluster-scoped resources.
			},
		}
	}

	// If any node changes, we request a reconciliation for every single ElaraScaler object.
	return requests
}

// fetchAllTargetDeployments gathers information about all deployments defined in the spec.
func (r *ElaraScalerReconciler) fetchAllTargetDeployments(ctx context.Context, logger logr.Logger, scaler scalingv1alpha1.ElaraScaler) (*scalingEntities, error) {
	entities := &scalingEntities{
		Deployments: make(map[types.NamespacedName]*deploymentInfo),
		Independent: []types.NamespacedName{},
		Groups:      make(map[string][]types.NamespacedName),
	}

	allTargets := []scalingv1alpha1.DeploymentTargetSpec{}
	groupMembers := make(map[types.NamespacedName]scalingv1alpha1.GroupMemberSpec)

	// Collect all unique deployments from independent and grouped definitions
	for _, indep := range scaler.Spec.IndependentDeployments {
		allTargets = append(allTargets, indep.DeploymentTargetSpec)
	}
	for _, group := range scaler.Spec.DeploymentGroups {
		entities.Groups[group.Name] = []types.NamespacedName{}
		for _, member := range group.Members {
			key := types.NamespacedName{Name: member.Name, Namespace: member.Namespace}
			allTargets = append(allTargets, member.DeploymentTargetSpec)
			groupMembers[key] = member
			entities.Groups[group.Name] = append(entities.Groups[group.Name], key)
		}
	}

	// For each unique deployment, fetch its live state
	for _, target := range allTargets {
		key := types.NamespacedName{Name: target.Name, Namespace: target.Namespace}

		// Avoid processing duplicates
		if _, exists := entities.Deployments[key]; exists {
			continue
		}

		deployment := &appsv1.Deployment{}
		if err := r.Get(ctx, key, deployment); err != nil {
			logger.Error(err, "Failed to get target deployment", "deployment", key.String())
			// We continue here, effectively ignoring a missing deployment for this cycle.
			// This makes the controller more resilient if a deployment is temporarily unavailable.
			continue
		}

		info := &deploymentInfo{
			Name:            target.Name,
			Namespace:       target.Namespace,
			Min:             target.MinReplicas,
			Max:             target.MaxReplicas,
			CurrentReplicas: *deployment.Spec.Replicas,
			Ref:             deployment,
		}

		if member, isGrouped := groupMembers[key]; isGrouped {
			info.Weight = member.Weight
		} else {
			entities.Independent = append(entities.Independent, key)
		}

		entities.Deployments[key] = info
	}

	return entities, nil
}

// largestRemainderMethod distributes a total integer amount among a set of items
// based on their float proportions, ensuring the sum of distributed integers equals the total.
func largestRemainderMethod(proportions map[string]float64, total int) map[string]int {
	type item struct {
		name      string
		floor     int
		remainder float64
	}

	var items []item
	distributedSum := 0
	for name, prop := range proportions {
		val := prop * float64(total)
		floor := int(math.Floor(val))
		remainder := val - float64(floor)
		items = append(items, item{name: name, floor: floor, remainder: remainder})
		distributedSum += floor
	}

	// Sort items by remainder in descending order to give the "largest remainders" priority.
	sort.Slice(items, func(i, j int) bool {
		return items[i].remainder > items[j].remainder
	})

	remainderToDistribute := total - distributedSum
	result := make(map[string]int)
	for i := range items {
		result[items[i].name] = items[i].floor
		if remainderToDistribute > 0 {
			result[items[i].name]++
			remainderToDistribute--
		}
	}

	return result
}

// applyConstraintsAndRedistribute takes the desired changes and adjusts them based on
// min/max replica constraints, redistributing any surplus or deficit.
func (r *ElaraScalerReconciler) applyConstraintsAndRedistribute(
	logger logr.Logger,
	entities *scalingEntities,
	desiredChanges map[types.NamespacedName]int,
) map[types.NamespacedName]int {

	finalChanges := make(map[types.NamespacedName]int)
	initialTotalChange := 0
	for _, change := range desiredChanges {
		initialTotalChange += change
	}

	if initialTotalChange == 0 {
		return finalChanges
	}

	// 1. Initial Application & Clamping
	var discrepancy int // Positive for surplus (scale-up), negative for deficit (scale-down)

	for key, depInfo := range entities.Deployments {
		desiredChange := desiredChanges[key]

		// Clamp the change to respect min/max boundaries
		newReplicas := depInfo.CurrentReplicas + int32(desiredChange)
		clampedReplicas := max(depInfo.Min, min(depInfo.Max, newReplicas))

		actualChange := int(clampedReplicas - depInfo.CurrentReplicas)
		finalChanges[key] = actualChange

		// Accumulate the discrepancy
		discrepancy += (desiredChange - actualChange)
	}

	logger.Info("Applied initial constraints", "desiredChanges", desiredChanges, "clampedChanges", finalChanges, "discrepancy", discrepancy)

	// 2. Iterative Redistribution
	isScaleDown := initialTotalChange < 0

	if isScaleDown {
		// Redistribute a deficit (discrepancy < 0)
		for discrepancy < 0 {
			// Find the best "donor" deployment to take an extra reduction.
			// The best donor has the largest margin to its minimum.
			var bestDonorKey types.NamespacedName
			maxMargin := int32(-1)

			for key, depInfo := range entities.Deployments {
				currentReplicasAfterChange := depInfo.CurrentReplicas + int32(finalChanges[key])
				margin := currentReplicasAfterChange - depInfo.Min
				if margin > maxMargin {
					maxMargin = margin
					bestDonorKey = key
				}
			}

			// If no deployment can be scaled down further, we must stop.
			if maxMargin <= 0 {
				logger.Info("Could not redistribute full deficit, no deployments have room to scale down further.", "remainingDeficit", discrepancy)
				break
			}

			// Apply one more reduction to the best donor.
			finalChanges[bestDonorKey]--
			discrepancy++
			logger.Info("Redistributing one deficit replica", "to", bestDonorKey.String())
		}
	} else {
		// Redistribute a surplus (discrepancy > 0)
		for discrepancy > 0 {
			// Find the best "receiver" deployment to take an extra addition.
			// The best receiver has the largest capacity remaining.
			var bestReceiverKey types.NamespacedName
			maxCapacity := int32(-1)

			for key, depInfo := range entities.Deployments {
				currentReplicasAfterChange := depInfo.CurrentReplicas + int32(finalChanges[key])
				capacity := depInfo.Max - currentReplicasAfterChange
				if capacity > maxCapacity {
					maxCapacity = capacity
					bestReceiverKey = key
				}
			}

			// If no deployment can be scaled up further, we must stop.
			if maxCapacity <= 0 {
				logger.Info("Could not redistribute full surplus, no deployments have room to scale up further.", "remainingSurplus", discrepancy)
				break
			}

			// Apply one more addition to the best receiver.
			finalChanges[bestReceiverKey]++
			discrepancy--
			logger.Info("Redistributing one surplus replica", "to", bestReceiverKey.String())
		}
	}

	return finalChanges
}

// Helper function to format map[types.NamespacedName]int for logging
func formatDeploymentChangesForLog(changes map[types.NamespacedName]int) map[string]int {
	loggable := make(map[string]int)
	for k, v := range changes {
		loggable[k.String()] = v
	}
	return loggable
}
