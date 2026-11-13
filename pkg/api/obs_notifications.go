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
	// Optional for compatibility with servers released before receiver evidence.
	// State above retains its aggregate assessment; this section is independent.
	ReceiverHealth *ObsNotificationReceiverHealth `json:"receiver_health,omitempty"`
}

// ObsNotificationReceiverHealth observes deployment intent and completed
// transport requests. It never establishes per-alert or human receipt.
type ObsNotificationReceiverHealth struct {
	State       string                    `json:"state"`
	Reason      string                    `json:"reason,omitempty"`
	Collector   ObsNotificationSignal     `json:"collector"`
	Inventory   ObsNotificationSignal     `json:"inventory"`
	Groups      []ObsNotificationReceiver `json:"groups"`
	Limitations []string                  `json:"limitations"`
}

// ObsNotificationReceiver names a reviewed receiver/integration group, never
// an address. Expected is nil when deployment intent cannot be established.
type ObsNotificationReceiver struct {
	Receiver    string                `json:"receiver"`
	Integration string                `json:"integration"`
	Expected    *bool                 `json:"expected"`
	State       string                `json:"state"`
	Reason      string                `json:"reason,omitempty"`
	Requests    ObsNotificationSignal `json:"requests"`
	Failures    ObsNotificationSignal `json:"failures"`
	// Value is the Unix-second clock when the collector confirmed success.
	// ObservedAt is the freshness of that gauge, not the delivery time.
	LastSuccessObservation ObsNotificationSignal `json:"last_success_observation"`
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
