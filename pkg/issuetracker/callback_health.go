package issuetracker

import "time"

// CallbackHealth describes retained, verified delivery hints for the current
// connection and ticket. Linear diagnostics count rejected attempts against the configured endpoint; they
// do not attribute the request to the provider or prove
// that a provider webhook is registered.
type CallbackHealth struct {
	Provider                                                                      string
	Configured                                                                    bool
	LastVerifiedAt, LastRejectedAt                                                *time.Time
	InvalidSignature, WrongScope, StaleDelivery, InvalidPayload, DeliveryConflict int64
	Setup                                                                         string
	RetainedDeliveries, PendingHints                                              int64
	LastReceivedAt, LastProcessedAt                                               *time.Time
}
