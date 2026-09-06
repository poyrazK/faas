// Package reqbudget carries an end-to-end request wall-clock budget
// through context.Context. The platform installs a Budget on every
// customer-facing request at the edge (gatewayd-public / apid), and
// every downstream call (DB, gRPC, outbound HTTP) derives its own
// deadline as min(parentRemaining - overhead, ownCeiling). The
// deadline can only ever shrink, never grow, so a slow hop
// short-circuits the rest of the chain instead of consuming the full
// server timeout.
//
// The Budget travels as a value in context, attached by
// WithRemaining at the edge and reshaped by WithOverhead / WithCeiling
// at each downstream hop. When the inbound ctx has no Budget (an
// internal goroutine, an admin path), WithOverhead and WithCeiling
// are identity no-ops so existing call-sites are unaffected.
// Long-lived responses use WithStream: the request budget remains active
// until response headers are committed, then detach drops only that budget
// while retaining the original client/server cancellation root.
//
// Source of truth for default values lives in pkg/api/limits.go
// (RequestBudgetDefault, RequestBudgetMax, RequestBudgetApidDefault,
// DefaultOverheadDB/GRPC/HTTP/Stream/Queue). This package re-exports
// the same constants under reqbudget.Default* so callers don't have to
// import pkg/api for a hop reservation.
//
// See ADR-093 for the design.
package reqbudget
