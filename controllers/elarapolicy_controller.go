package controllers

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	// IMPORTANT: Ensure this path matches your module name in go.mod
	//"elara/api/v1"
	greenopsv1 "elara/api/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Constants for the node labels used to determine power.
const (
	optimalPowerLabel   = "rapl/optimal"
	currentPowerLabel   = "rapl/current"
	masterNodeRoleLabel = "node-role.kubernetes.io/master"
)

// ElaraPolicyReconciler reconciles a ElaraPolicy object.
type ElaraPolicyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// --- Scaling Logic (ported from simulation) ---

// DeclarativeScaler holds the scaling logic. It's stateless.
type DeclarativeScaler struct {
	Deployments []greenopsv1.ManagedDeployment
}

// TargetState represents the desired final state for a single deployment.
type TargetState struct {
	Name          string
	Namespace     string
	FinalReplicas int32
}

// +kubebuilder:rbac:groups=greenops.elara.dev,resources=elarapolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// This is the correct way to specify cluster-wide permissions for the controller.
// The rules below will be aggregated into a single ClusterRole.
// We need cluster-wide access to Nodes to read power labels.
// We need cluster-wide access to Deployments to scale them in any namespace.
// We need cluster-wide access to our ElaraPolicy custom resource.
func (r *ElaraPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Starting reconciliation cycle", "policy", req.Name)

	// Step 1: Fetch the ElaraPolicy resource.
	var policy greenopsv1.ElaraPolicy
	if err := r.Get(ctx, req.NamespacedName, &policy); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ElaraPolicy resource not found. It might have been deleted. Ignoring.")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get ElaraPolicy")
		return ctrl.Result{}, err
	}

	// Step 2: Calculate cluster power by reading Node labels.
	optimalPower, currentPower, err := r.calculateClusterPower(ctx)
	if err != nil {
		logger.Error(err, "Failed to calculate cluster power metrics")
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}
	if optimalPower == 0 {
		logger.Error(nil, "Calculated Optimal Power is 0. Check 'rapl/optimal' labels on worker nodes.")
		// We requeue to try again later, in case labels are being added.
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}

	reductionPercentage := (optimalPower - currentPower) / optimalPower
	logger.Info("Calculated power metrics from nodes", "Optimal", optimalPower, "Current", currentPower, "Reduction", fmt.Sprintf("%.2f%%", reductionPercentage*100))

	// Step 3: Calculate the target state for all managed deployments.
	scaler := &DeclarativeScaler{Deployments: policy.Spec.Deployments}
	targetStates := scaler.CalculateTargetState(reductionPercentage)

	// Step 4: Apply the calculated target state to the actual Deployments in the cluster.
	for _, target := range targetStates {
		var deployment appsv1.Deployment
		err := r.Get(ctx, types.NamespacedName{Name: target.Name, Namespace: target.Namespace}, &deployment)
		if err != nil {
			logger.Error(err, "Failed to find managed deployment, skipping.", "deployment", target.Name, "namespace", target.Namespace)
			continue
		}

		if deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == target.FinalReplicas {
			logger.Info("Deployment is already at the target state", "deployment", target.Name, "replicas", target.FinalReplicas)
			continue
		}

		logger.Info("ACTION: Updating deployment replicas", "deployment", target.Name, "current", *deployment.Spec.Replicas, "target", target.FinalReplicas)
		deployment.Spec.Replicas = &target.FinalReplicas
		if err := r.Update(ctx, &deployment); err != nil {
			logger.Error(err, "Failed to update deployment", "deployment", target.Name)
			// Continue to the next deployment even if one fails.
		}
	}

	logger.Info("Reconciliation cycle finished successfully")
	// Requeue periodically to react to power changes on nodes.
	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
}

// calculateClusterPower lists worker nodes and aggregates power values from their labels.
func (r *ElaraPolicyReconciler) calculateClusterPower(ctx context.Context) (optimal float64, current float64, err error) {
	logger := log.FromContext(ctx)
	nodeList := &corev1.NodeList{}

	requirement, err := labels.NewRequirement(masterNodeRoleLabel, selection.DoesNotExist, nil)
	if err != nil {
		// This should not happen for a static requirement.
		return 0, 0, fmt.Errorf("failed to create node label requirement: %w", err)
	}

	// Create a selector from the requirement.
	selector := labels.NewSelector().Add(*requirement)

	// Use the selector in ListOptions to filter the nodes.
	if err := r.List(ctx, nodeList, &client.ListOptions{LabelSelector: selector}); err != nil {
		return 0, 0, fmt.Errorf("failed to list worker nodes: %w", err)
	}

	if len(nodeList.Items) == 0 {
		logger.Info("No worker nodes found in the cluster.")
	}

	for _, node := range nodeList.Items {
		optimalStr, ok1 := node.Labels[optimalPowerLabel]
		currentStr, ok2 := node.Labels[currentPowerLabel]

		if !ok1 || !ok2 {
			logger.V(1).Info("Node is missing required power labels, skipping.", "node", node.Name)
			continue
		}

		opt, err := strconv.ParseFloat(optimalStr, 64)
		if err != nil {
			logger.V(1).Info("Invalid 'rapl/optimal' label on node, skipping.", "node", node.Name, "value", optimalStr)
			continue
		}

		cur, err := strconv.ParseFloat(currentStr, 64)
		if err != nil {
			logger.V(1).Info("Invalid 'rapl/current' label on node, skipping.", "node", node.Name, "value", currentStr)
			continue
		}

		optimal += opt
		current += cur
	}
	return optimal, current, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ElaraPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&greenopsv1.ElaraPolicy{}).
		// Add a Watch for Node objects.
		// Any change to a Node will trigger a reconciliation of ALL ElaraPolicy objects.
		// Since we expect only one policy, this effectively wakes up the controller.
		Watches(
			&corev1.Node{},
			handler.EnqueueRequestsFromMapFunc(r.mapNodeChangesToPolicy),
		).
		Complete(r)
}

// mapNodeChangesToPolicy is a mapping function that triggers a reconciliation
// for all ElaraPolicy objects whenever a Node changes.
func (r *ElaraPolicyReconciler) mapNodeChangesToPolicy(ctx context.Context, obj client.Object) []reconcile.Request {
	logger := log.FromContext(ctx)
	logger.Info("Node change detected, triggering policy reconciliation", "node", obj.GetName())

	policyList := &greenopsv1.ElaraPolicyList{}
	if err := r.List(ctx, policyList); err != nil {
		logger.Error(err, "Failed to list ElaraPolicies to trigger reconciliation")
		return []reconcile.Request{}
	}

	requests := make([]reconcile.Request, len(policyList.Items))
	for i, item := range policyList.Items {
		requests[i] = reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name: item.GetName(),
			},
		}
	}
	return requests
}

// CalculateTargetState computes the final state of all deployments based on the current power reduction.
func (s *DeclarativeScaler) CalculateTargetState(reductionPercentage float64) []TargetState {
	// If power is optimal, return max replicas.
	if reductionPercentage <= 0 {
		return s.getOptimalState()
	}

	// Step 1: Calculate global reduction target from the absolute maximum.
	totalMaxReplicas := 0
	for _, d := range s.Deployments {
		totalMaxReplicas += int(d.MaxReplicas)
	}
	globalReductionTarget := int(math.Ceil(float64(totalMaxReplicas) * reductionPercentage))

	// Step 2: Distribute reduction among entities (groups and independents).
	// This part is complex and must be exactly right.

	groups := make(map[string][]greenopsv1.ManagedDeployment)
	var independent []greenopsv1.ManagedDeployment
	for _, d := range s.Deployments {
		if d.Group != "" {
			groups[d.Group] = append(groups[d.Group], d)
		} else {
			independent = append(independent, d)
		}
	}

	type entityInfo struct {
		name         string
		maxPotential int
	}
	var entities []entityInfo
	totalEntityPotential := 0

	for name, members := range groups {
		potential := 0
		for _, m := range members {
			potential += int(m.MaxReplicas)
		}
		entities = append(entities, entityInfo{name: name, maxPotential: potential})
		totalEntityPotential += potential
	}
	for _, d := range independent {
		entities = append(entities, entityInfo{name: d.Name, maxPotential: int(d.MaxReplicas)})
		totalEntityPotential += int(d.MaxReplicas)
	}

	entityShares := make([]float64, len(entities))
	for i, e := range entities {
		if totalEntityPotential > 0 {
			entityShares[i] = float64(e.maxPotential) / float64(totalEntityPotential) * float64(globalReductionTarget)
		}
	}
	entityAllocations := largestRemainderMethod(entityShares, globalReductionTarget)

	// Step 3: Calculate desired reductions for each individual deployment.
	desiredReductions := make(map[string]int) // key: namespace/name

	for i, e := range entities {
		alloc := entityAllocations[i]
		members, isGroup := groups[e.name]
		if isGroup {
			totalWeight := 0.0
			for _, m := range members {
				totalWeight += m.Weight.AsApproximateFloat64()
			}

			memberShares := make([]float64, len(members))
			for j, m := range members {
				if totalWeight > 0 {
					memberShares[j] = float64(alloc) * m.Weight.AsApproximateFloat64() / totalWeight
				}
			}
			memberAllocations := largestRemainderMethod(memberShares, alloc)
			for j, mAlloc := range memberAllocations {
				key := fmt.Sprintf("%s/%s", members[j].Namespace, members[j].Name)
				desiredReductions[key] = mAlloc
			}
		} else {
			// Find the independent deployment
			for _, d := range independent {
				if d.Name == e.name {
					key := fmt.Sprintf("%s/%s", d.Namespace, d.Name)
					desiredReductions[key] = alloc
					break
				}
			}
		}
	}

	// Step 4: Apply minReplicas constraints and calculate initial deficit.
	finalReductions := make(map[string]int)
	totalDeficit := 0

	for _, d := range s.Deployments {
		key := fmt.Sprintf("%s/%s", d.Namespace, d.Name)
		maxPossibleReduction := int(d.MaxReplicas - d.MinReplicas)
		desired := desiredReductions[key]

		actualReduction := min(desired, maxPossibleReduction)
		finalReductions[key] = actualReduction
		totalDeficit += desired - actualReduction
	}

	// Step 5: Redistribute deficit.
	for totalDeficit > 0 {
		type donorInfo struct {
			key    string
			margin int
		}
		var donors []donorInfo
		for _, d := range s.Deployments {
			key := fmt.Sprintf("%s/%s", d.Namespace, d.Name)
			maxPossibleReduction := int(d.MaxReplicas - d.MinReplicas)
			currentReduction := finalReductions[key]
			margin := maxPossibleReduction - currentReduction
			if margin > 0 {
				donors = append(donors, donorInfo{key: key, margin: margin})
			}
		}

		if len(donors) == 0 {
			log.Log.V(0).Info("Warning: Could not redistribute remaining deficit", "deficit", totalDeficit)
			break
		}

		sort.Slice(donors, func(i, j int) bool { return donors[i].margin > donors[j].margin })

		bestDonorKey := donors[0].key
		finalReductions[bestDonorKey]++
		totalDeficit--
	}

	// Step 6: Calculate final replica counts.
	finalStates := make([]TargetState, len(s.Deployments))
	for i, d := range s.Deployments {
		key := fmt.Sprintf("%s/%s", d.Namespace, d.Name)
		finalReplicas := d.MaxReplicas - int32(finalReductions[key])
		finalStates[i] = TargetState{Name: d.Name, Namespace: d.Namespace, FinalReplicas: finalReplicas}
	}
	return finalStates
}

// getOptimalState returns the state where all deployments are at their maxReplicas.
func (s *DeclarativeScaler) getOptimalState() []TargetState {
	states := make([]TargetState, len(s.Deployments))
	for i, d := range s.Deployments {
		states[i] = TargetState{Name: d.Name, Namespace: d.Namespace, FinalReplicas: d.MaxReplicas}
	}
	return states
}

// largestRemainderMethod fairly distributes an integer total based on float shares.
func largestRemainderMethod(shares []float64, totalInt int) []int {
	integerParts := make([]int, len(shares))
	remainders := make([]float64, len(shares))
	for i, s := range shares {
		integerParts[i] = int(math.Floor(s))
		remainders[i] = s - float64(integerParts[i])
	}

	currentTotal := 0
	for _, p := range integerParts {
		currentTotal += p
	}
	remaining := totalInt - currentTotal

	type remainderItem struct {
		index     int
		remainder float64
	}
	remainderItems := make([]remainderItem, len(remainders))
	for i, r := range remainders {
		remainderItems[i] = remainderItem{index: i, remainder: r}
	}

	sort.Slice(remainderItems, func(i, j int) bool { return remainderItems[i].remainder > remainderItems[j].remainder })

	for i := 0; i < remaining; i++ {
		integerParts[remainderItems[i].index]++
	}
	return integerParts
}

// min is a helper function to find the minimum of two integers.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
