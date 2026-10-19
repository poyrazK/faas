package api

import "time"

// ObsNotificationHealth describes pipeline evidence, never receipt by a person
// or delivery of a particular episode. Counter increases are extrapolated.
type ObsNotificationHealth struct {
	GeneratedAt     time.Time                        `json:"generated_at"`
	State           string                           `json:"state"`
	WindowSeconds   int                              `json:"window_seconds"`
	AlertmanagerAPI ObsNotificationAPIStatus         `json:"alertmanager_api"`
	Signals         map[string]ObsNotificationSignal `json:"signals"`
	Integrations    []ObsNotificationIntegration     `json:"integrations"`
	Limitations     []string                         `json:"limitations"`
}

// ObsNotificationSignal separates fresh zero values from missing evidence.
// Healthy is evidence quality; a fresh failure count can still be positive.
type ObsNotificationSignal struct {
	State         string     `json:"state"`
	Reason        string     `json:"reason,omitempty"`
	Value         *float64   `json:"value"`
	ObservedAt    *time.Time `json:"observed_at,omitempty"`
	WindowSeconds int        `json:"window_seconds"`
	MaxAgeSeconds int        `json:"max_age_seconds"`
}

type ObsNotificationAPIStatus struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason,omitempty"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
}

// Integration is a closed integration type, never a receiver name or address.
type ObsNotificationIntegration struct {
	Integration string                `json:"integration"`
	Attempts    ObsNotificationSignal `json:"attempts"`
	Failures    ObsNotificationSignal `json:"failures"`
}

// ObsAlertSuppression is current evidence attached only to platform-alert
// incident evidence. Nil booleans mean the suppression state is unknown.
type ObsAlertSuppression struct {
	State     string     `json:"state"`
	Reason    string     `json:"reason,omitempty"`
	CheckedAt time.Time  `json:"checked_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
	Silenced  *bool      `json:"silenced"`
	Inhibited *bool      `json:"inhibited"`
}
