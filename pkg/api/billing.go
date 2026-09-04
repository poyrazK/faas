// Billing-related API DTOs that live in pkg/api so the JSON wire
// shape is decoupled from the provider-specific package.
//
// PR-P3: BillingReconcileResponse is the response shape for POST
// /v1/admin/billing-reconcile/{id}. The handler is in
// cmd/apid/handlers_admin_billing.go and reads usage_minutes via
// billing.Provider.ReconcileUsage; the response surfaces the
// total mb_seconds for the [start, end) window so operators can
// diff the SDK-side number against the local usage_minutes sum.
//
// Money: integer mb_seconds. No floats anywhere on this struct —
// the underlying values are integer counters summed across
// usage_minutes rows.
package api

import "time"

// BillingReconcileResponse is the JSON shape POST
// /v1/admin/billing-reconcile/{id} returns on success (200).
//
// AccountID echoes the path value so the CLI's loop output is
// self-describing without parsing the URL. Start / End are RFC 3339
// timestamps in UTC, formatted as 2006-01-02T15:04:05Z07:00 by the
// handler. MBSeconds is the integer total the provider SDK returned
// for the window; for Stripe this is the usage-record sum, for
// Paddle the handler 501s (provider returns ErrNotImplemented).
type BillingReconcileResponse struct {
	AccountID string    `json:"account_id"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	MBSeconds int64     `json:"mb_seconds"`
}

// BillingCatalogKind discriminates the entries inside BillingCatalogResponse.
// Providers may expose product handles only (Polar) or product plus monthly
// and overage price handles (Paddle).
type BillingCatalogKind string

const (
	BillingCatalogKindMonthly BillingCatalogKind = "monthly"
	BillingCatalogKindOverage BillingCatalogKind = "overage"
	BillingCatalogKindProduct BillingCatalogKind = "product"
)

// BillingCatalogEntry is one row in the provider product/price catalog.
// Plan values are the billable api.Plan constants ("hobby", "pro",
// "scale") — PlanFree is intentionally absent because it carries
// no recurring line item. Handle is the provider-side product or price ID.
// SyncedAt is RFC 3339 UTC from the provider's last successful preflight;
// the zero-value renders as "0001-01-01T00:00:00Z" via the standard JSON
// marshaler.
type BillingCatalogEntry struct {
	Plan     string             `json:"plan"`
	Kind     BillingCatalogKind `json:"kind"`
	Handle   string             `json:"handle"`
	SyncedAt time.Time          `json:"synced_at"`
}

// BillingCatalogResponse is the wire shape for the
// GET/POST/DELETE /v1/admin/billing-paddle-catalog compatibility endpoints.
// Provider is the active billing provider's name (polar / paddle). Providers
// without a catalog surface receive a 501 before this struct is serialized.
// SyncedAt is the timestamp of the most recent successful catalog preflight;
// it is empty when no hydration has completed.
type BillingCatalogResponse struct {
	Provider string                `json:"provider"`
	SyncedAt string                `json:"synced_at"`
	Entries  []BillingCatalogEntry `json:"entries"`
}

// BillingPaddleOveragePreflightResponse is the wire shape for GET
// /v1/admin/billing-paddle-overage/preflight. Operator-only;
// consumed by `faas billing reconcile-paddle-overage`. Mirrors
// state.PaddleOverageDedupeSchemaResult's booleans but lives
// here so the JSON wire shape is decoupled from the store's
// in-process shape — the pre-flight is a CLI concern, not a
// state.Store concern, even though the probe reuses the
// store's read path.
//
// TableExists is false when no Paddle overage flushes have ever
// run against this DB (migrations 00034 + 00041 both unapplied).
// The four HasX columns are the migration-00041 additions;
// the CLI reports each missing column by name so an operator on
// a partially-applied DB sees exactly what to fix.
//
// PendingRows / CompletedRows are informational (per-state
// counts) — useful as a dashboard-shape read and a way to see
// whether the meterd loop has any in-flight claims to reap.
type BillingPaddleOveragePreflightResponse struct {
	TableExists    bool  `json:"table_exists"`
	HasWindowStart bool  `json:"has_window_start"`
	HasState       bool  `json:"has_state"`
	HasClaimedAt   bool  `json:"has_claimed_at"`
	HasClaimedBy   bool  `json:"has_claimed_by"`
	PendingRows    int64 `json:"pending_rows"`
	CompletedRows  int64 `json:"completed_rows"`
}

// AdminRefundResponse is the wire result of an operator-initiated refund.
// The account and local invoice identifiers are echoed so an automation can
// correlate the provider refund with the Gregale record without re-reading
// the request body.
type AdminRefundResponse struct {
	AccountID        string `json:"account_id"`
	InvoiceID        string `json:"invoice_id"`
	Provider         string `json:"provider"`
	ProviderRefundID string `json:"provider_refund_id"`
	ChargeID         string `json:"charge_id"`
	AmountCents      int64  `json:"amount_cents"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
}
