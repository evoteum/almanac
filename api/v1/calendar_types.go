package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CalendarInstance is a one-shot or year-specific absolute time window.
// Use this for events whose dates change each year (e.g. Easter, Cheltenham
// Festival) or events that occur only once.
type CalendarInstance struct {
	// +required
	Start metav1.Time `json:"start"`
	// +required
	End metav1.Time `json:"end"`
}

// RecurringWindow is a time window defined by cron expressions, repeating on
// the schedule implied by the expressions. Use this for fixed-date annual
// events (e.g. Christmas, New Year) where the date never changes.
type RecurringWindow struct {
	// Start is a cron expression for when the window opens, e.g. "0 0 25 12 *".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=9
	Start string `json:"start"`

	// End is a cron expression for when the window closes, e.g. "0 0 27 12 *".
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=9
	End string `json:"end"`

	// Timezone is the IANA timezone for the cron expressions, e.g. "Europe/London".
	// +kubebuilder:default=UTC
	// +optional
	Timezone string `json:"timezone,omitempty"`
}

type CalendarSpec struct {
	// Instances are absolute time windows. Default and required for most events.
	// +optional
	Instances []CalendarInstance `json:"instances,omitempty"`

	// Recurring are cron-expression windows for fixed-date annually repeating events.
	// These are passed directly to KEDA cron triggers.
	// +optional
	Recurring []RecurringWindow `json:"recurring,omitempty"`
}

// CalendarStatus defines the observed state of Calendar.
type CalendarStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// Calendar is the Schema for the calendars API
type Calendar struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec CalendarSpec `json:"spec"`

	// +optional
	Status CalendarStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CalendarList contains a list of Calendar
type CalendarList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Calendar `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Calendar{}, &CalendarList{})
}
