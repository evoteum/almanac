package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type CalendarScaleTarget struct {
	// +kubebuilder:validation:MinLength=1
	DeploymentName string `json:"deploymentName"`

	// +kubebuilder:validation:Minimum=0
	Replicas int32 `json:"replicas"`
}

type CalendarScaleSpec struct {
	// CalendarName is the name of the cluster-scoped Calendar to reference.
	// +kubebuilder:validation:MinLength=1
	CalendarName string `json:"calendarName"`

	// Targets lists the Deployments to scale and their desired replica counts
	// during active Calendar windows.
	// +kubebuilder:validation:MinItems=1
	Targets []CalendarScaleTarget `json:"targets"`

	// Suspend pauses all reconciliation when true. Any managed ScaledObjects
	// are removed until suspend is cleared.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// CalendarScaleStatus defines the observed state of CalendarScale.
type CalendarScaleStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Calendar",type=string,JSONPath=`.spec.calendarName`
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.conditions[?(@.type=="Active")].status`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Suspended",type=boolean,JSONPath=`.spec.suspend`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CalendarScale is the Schema for the calendarscales API
type CalendarScale struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CalendarScaleSpec `json:"spec"`

	// +optional
	Status CalendarScaleStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CalendarScaleList contains a list of CalendarScale
type CalendarScaleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CalendarScale `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CalendarScale{}, &CalendarScaleList{})
}
