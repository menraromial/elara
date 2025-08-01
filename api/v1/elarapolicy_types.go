package v1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ManagedDeployment defines the configuration for a single managed deployment.
type ManagedDeployment struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	MinReplicas int32             `json:"minReplicas"`
	MaxReplicas int32             `json:"maxReplicas"`
	// +kubebuilder:default="1.0"
	Weight      resource.Quantity `json:"weight,omitempty"`
	Group       string            `json:"group,omitempty"`
}

// ElaraPolicySpec defines the desired state of ElaraPolicy.
type ElaraPolicySpec struct {
	Deployments []ManagedDeployment `json:"deployments"`
}

// ElaraPolicyStatus defines the observed state of ElaraPolicy.
type ElaraPolicyStatus struct {}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster,shortName=ep
//+kubebuilder:subresource:status
type ElaraPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ElaraPolicySpec   `json:"spec,omitempty"`
	Status            ElaraPolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true
type ElaraPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ElaraPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ElaraPolicy{}, &ElaraPolicyList{})
}