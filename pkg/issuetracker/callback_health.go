package issuetracker

import "time"

// CallbackHealth describes retained, verified delivery hints for the current
// connection and ticket. It does not count unauthenticated requests or prove
// that a provider webhook is registered.
type CallbackHealth struct {
	RetainedDeliveries, PendingHints int64
	LastReceivedAt, LastProcessedAt  *time.Time
}
