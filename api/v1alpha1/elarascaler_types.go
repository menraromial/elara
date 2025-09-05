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

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ElaraScalerSpec defines the desired state of an ElaraScaler policy.
type ElaraScalerSpec struct {
	// --- GLOBAL SETTINGS ---

	// DryRun mode prevents the controller from making any actual scaling changes.
	// It will only log the actions it would have taken.
	// +optional
	DryRun bool `json:"dryRun,omitempty"`

	// --- CONTROLLER BEHAVIOR (Phase 1) ---

	// Deadband is the minimal relative change in power required to trigger a scaling evaluation.
	// Corresponds to δ in the model. Represented as a string, e.g., "0.05" for 5%.
	// +kubebuilder:validation:Required
	Deadband resource.Quantity `json:"deadband"`

	// StabilizationWindow contains settings for the anti-flapping mechanism.
	// +kubebuilder:validation:Required
	StabilizationWindow StabilizationWindowSpec `json:"stabilizationWindow"`

	// --- TARGETS DEFINITION ---

	// DeploymentGroups defines groups of deployments that are scaled together.
	// Corresponds to G in the model.
	// +optional
	DeploymentGroups []DeploymentGroupSpec `json:"deploymentGroups,omitempty"`

	// IndependentDeployments defines deployments that are scaled independently.
	// Corresponds to D_I in the model.
	// +optional
	IndependentDeployments []IndependentDeploymentSpec `json:"independentDeployments,omitempty"`
}

// StabilizationWindowSpec defines the time windows and tolerances for scaling decisions.
type StabilizationWindowSpec struct {
	// Increase duration for the stabilization window before scaling up. E.g., "1m", "30s".
	// Corresponds to W_inc.
	// +kubebuilder:validation:Required
	Increase metav1.Duration `json:"increase"`

	// Decrease duration for the stabilization window before scaling down. E.g., "5m".
	// Corresponds to W_dec.
	// +kubebuilder:validation:Required
	Decrease metav1.Duration `json:"decrease"`

	// IncreaseTolerance is the allowed relative power fluctuation during the increase window.
	// Corresponds to ε_inc. Represented as a string, e.g., "0.02" for 2%.
	// +kubebuilder:validation:Required
	IncreaseTolerance resource.Quantity `json:"increaseTolerance"`

	// DecreaseTolerance is the allowed relative power fluctuation during the decrease window.
	// Corresponds to ε_dec. Represented as a string, e.g., "0.02" for 2%.
	// +kubebuilder:validation:Required
	DecreaseTolerance resource.Quantity `json:"decreaseTolerance"`
}

// DeploymentTargetSpec defines how to select and configure a deployment.
type DeploymentTargetSpec struct {
	// Name of the target Kubernetes Deployment.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace of the target Kubernetes Deployment.
	// +kubebuilder:validation:Required
	Namespace string `json:"namespace"`

	// MinReplicas is the minimum number of replicas. Corresponds to N_d.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	MinReplicas int32 `json:"minReplicas"`

	// MaxReplicas is the maximum number of replicas. Corresponds to M_d.
	// +kubebuilder:validation:Required
	MaxReplicas int32 `json:"maxReplicas"`
}

// DeploymentGroupSpec defines a group of deployments with weighted scaling.
type DeploymentGroupSpec struct {
	// Name for the group, for identification purposes.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Members of the scaling group.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Members []GroupMemberSpec `json:"members"`
}

// GroupMemberSpec defines a deployment within a group, including its weight.
type GroupMemberSpec struct {
	DeploymentTargetSpec `json:",inline"`

	// Weight for distributing scaling actions within the group. Corresponds to w_d.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Weight int32 `json:"weight"`
}

// IndependentDeploymentSpec defines a deployment that is scaled on its own.
type IndependentDeploymentSpec struct {
	DeploymentTargetSpec `json:",inline"`
}

// ElaraScalerStatus defines the observed state of ElaraScaler.
type ElaraScalerStatus struct {
	// Conditions represent the latest available observations of the ElaraScaler's state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// --- STATE MACHINE STATE ---

	// Mode is the current state of the controller's state machine.
	// Corresponds to 'mode'. Can be "Stable", "PendingIncrease", "PendingDecrease".
	// +optional
	Mode string `json:"mode,omitempty"`

	// ReferencePower is the last power value (in Watts) that triggered a scaling action or state reset.
	// Corresponds to P_ref.
	// +optional
	ReferencePower *resource.Quantity `json:"referencePower,omitempty"`

	// TriggerTime is the timestamp when the state changed to a Pending state.
	// Corresponds to t_trigger.
	// +optional
	TriggerTime *metav1.Time `json:"triggerTime,omitempty"`

	// TriggerPower is the power value (in Watts) measured at TriggerTime.
	// Corresponds to P_trigger.
	// +optional
	TriggerPower *resource.Quantity `json:"triggerPower,omitempty"`

	// LastScaleTime is the timestamp of the last successful scaling action.
	// +optional
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:resource:scope=Cluster
//+kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".status.mode"
//+kubebuilder:printcolumn:name="Ref Power",type="string",JSONPath=".status.referencePower"
//+kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// ElaraScaler is the Schema for the elarascalers API
type ElaraScaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ElaraScalerSpec   `json:"spec,omitempty"`
	Status ElaraScalerStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ElaraScalerList contains a list of ElaraScaler
type ElaraScalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ElaraScaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ElaraScaler{}, &ElaraScalerList{})
}
