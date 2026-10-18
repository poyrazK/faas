package api

import "time"

// Notifications project current retained sources; read state is workspace-shared.
type ProductNotification struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	ReferenceID string    `json:"reference_id"`
	Feature     string    `json:"feature,omitempty"`
	HelpKind    string    `json:"help_kind,omitempty"`
	OccurredAt  time.Time `json:"occurred_at"`
	Revision    string    `json:"revision"`
	Read        bool      `json:"read"`
	ReadVersion int64     `json:"read_version"`
}

type ProductNotificationPage struct {
	AccountID      string                `json:"account_id"`
	AppID          string                `json:"app_id"`
	TenantID       string                `json:"tenant_id"`
	Items          []ProductNotification `json:"items"`
	UnreadCount    int64                 `json:"unread_count"`
	NextCursor     string                `json:"next_cursor,omitempty"`
	WindowStart    time.Time             `json:"window_start"`
	ObservedAt     time.Time             `json:"observed_at"`
	ReadStateScope string                `json:"read_state_scope"`
}

type ProductNotificationUpdate struct {
	ID              string `json:"id"`
	Revision        string `json:"revision"`
	ExpectedVersion *int64 `json:"expected_version"`
	Read            *bool  `json:"read"`
}
