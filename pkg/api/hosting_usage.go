package api

import "time"

// HostingUsage is catalog accounting, not a provider bucket inventory or bill.
// Reservations cover both file copies and a maximum-size manifest per release.
type HostingUsage struct {
	AccountID             string    `json:"account_id"`
	ReservedBytes         int64     `json:"reserved_bytes"`
	StorageLimitBytes     int64     `json:"storage_limit_bytes"`
	PayloadBytes          int64     `json:"payload_bytes"`
	ReadyPayloadBytes     int64     `json:"ready_payload_bytes"`
	RetiringReservedBytes int64     `json:"retiring_reserved_bytes"`
	PendingUploads        int64     `json:"pending_uploads"`
	PendingUploadsLimit   int64     `json:"pending_uploads_limit"`
	Releases              int64     `json:"releases"`
	ObservedAt            time.Time `json:"observed_at"`
}
