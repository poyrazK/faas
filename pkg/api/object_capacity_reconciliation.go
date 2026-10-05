package api

import "time"

// ObjectCapacityReconciliation reports a fenced inventory and quota rebase.
// Terminal blocked/failed jobs retain all reservations. Billing is unaffected.
type ObjectCapacityReconciliation struct {
	ID              string     `json:"id"`
	BucketID        string     `json:"bucket_id"`
	State           string     `json:"state"`
	InventoryScope  string     `json:"inventory_scope"`
	ScannedPages    int64      `json:"scanned_pages"`
	ScannedBytes    int64      `json:"scanned_bytes"`
	ScannedVersions int64      `json:"scanned_versions"`
	BeforeBytes     int64      `json:"before_bytes"`
	BeforeKeys      int64      `json:"before_keys"`
	AfterBytes      int64      `json:"after_bytes"`
	AfterKeys       int64      `json:"after_keys"`
	ReclaimedBytes  int64      `json:"reclaimed_bytes"`
	ReclaimedKeys   int64      `json:"reclaimed_keys"`
	PendingWrites   int64      `json:"pending_writes"`
	LastErrorCode   string     `json:"last_error_code,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}
