// PR-P3: Operator-facing admin surface for the billing Provider.
//
// The catalog handlers use billing.CatalogProvider, so the same operator
// surface works for the Polar public-release provider and the explicit
// Paddle provider. Providers without a catalog implementation receive a
// truthful 501 instead of an empty or misleading snapshot.
//
// Endpoints:
//
//	GET    /v1/admin/billing-paddle-catalog          → ListCatalog
//	POST   /v1/admin/billing-paddle-catalog/sync     → SyncCatalog
//	DELETE /v1/admin/billing-paddle-catalog          → ResetCatalog
//	POST   /v1/admin/billing-reconcile/{id}          → single-account reconcile
//
// All four sit behind `requireScope(api.ScopesAdminOnly...)` AND
// `s.adminAllows` — same two-layer gate as POST /v1/admin/accounts/
// {id}/credits (handlers_admin_credits.go:12). The scope check is
// declarative at the middleware level; the email allowlist is what
// stops a leaked admin key from a non-operator account from
// inspecting / mutating the catalog.
//
// The reconcile endpoint calls billing.Provider.ReconcileUsage
// directly. Stripe implements it (ADR-049 §B.1); Paddle returns
// billing.ErrNotImplemented (no usage-summary endpoint yet). The
// handler maps that sentinel to 501 so the surface is uniform
// across providers.

package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/state"
)

// billingOpsTimeout bounds the SyncCatalog handler. SDK round-trips
// against api.sandbox.paddle.com historically complete in < 5 s;
// 30 s is the fail-fast ceiling before the operator sees a 504.
// Mirrors the meterd pusher's reconcile-window shape.
const billingOpsTimeout = 30 * time.Second

// billingCatalogFor returns the provider-neutral catalog view or (nil,
// false) when the selected provider has no operator catalog surface.
func billingCatalogFor(p billing.Provider) (billing.CatalogProvider, bool) {
	if p == nil {
		return nil, false
	}
	ops, ok := p.(billing.CatalogProvider)
	return ops, ok
}

// providerName is the dispatcher's name resolver. Today the catalog
// surface is provider-specific; the loader already keeps the provider
// type on s.billingProvider and the package name is the canonical
// identity. A future PR-C that adds a third provider (LemonSqueezy
// stub) will extend this to a registry lookup.
func providerName(p billing.Provider) string {
	if p == nil {
		return ""
	}
	// Package-name dispatch via type assertion to the concrete providers.
	// Adding a provider means adding an else-if here; the catalogue
	// of provider names is small enough that a switch is overkill.
	if _, ok := p.(*paddle.Provider); ok {
		return "paddle"
	}
	if _, ok := p.(*polar.Provider); ok {
		return "polar"
	}
	// Stripe and any provider that does not satisfy the catalog surface
	// as "stripe" — providerName is only for the response body.
	// When a future provider (LemonSqueezy stub in PR-P5) joins, add
	// its type assertion here.
	return "stripe"
}

// nowUTC is the local time helper. cmd/apid's handlers_ext.go:1637
// uses time.Now().UTC() inline; this helper exists so admin
// handlers share one canonical format string.
func nowUTC() time.Time {
	return time.Now().UTC()
}

// paddleCatalogResponse is the wire shape returned by GET /v1/admin/
// billing-paddle-catalog. Wraps the entries in a top-level object
// so the response can carry provider metadata (last_sync_at,
// provider name) without flattening into the entries slice.
type paddleCatalogResponse struct {
	Provider string                    `json:"provider"`
	SyncedAt string                    `json:"synced_at"` // RFC 3339; "" when never synced
	Entries  []api.BillingCatalogEntry `json:"entries"`
}

// listPaddleCatalog handles GET /v1/admin/billing-paddle-catalog.
// Returns 200 + the catalog snapshot, 501 when the provider does
// not implement OpProvider, 403 on the admin allowlist.
//
// The endpoint is idempotent (read-only) and not gated by the
// idempotency middleware — there is no body and no mutation. A
// redelivered GET renders the same payload.
func (s *server) listPaddleCatalog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	ops, ok := billingCatalogFor(s.billingProvider)
	if !ok {
		writeProviderNotImplemented(w, "list", s.billingProvider)
		return
	}
	entries := ops.ListBillingCatalog(r.Context())

	// Surface SyncedAt as a top-level field too so the CLI can render
	// "last synced at <ts>" without scanning every entry. Picks the
	// first entry's SyncedAt (all entries carry the same stamp by
	// ListCatalog's contract — pinned by TestProperty_EnsurePlanProductsStampsLastSync).
	var syncedAt string
	if len(entries) > 0 {
		if !entries[0].SyncedAt.IsZero() {
			syncedAt = entries[0].SyncedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
	}
	if entries == nil {
		entries = []api.BillingCatalogEntry{}
	}
	writeJSON(w, http.StatusOK, paddleCatalogResponse{
		Provider: providerName(s.billingProvider),
		SyncedAt: syncedAt,
		Entries:  entries,
	})
}

// syncPaddleCatalog handles POST /v1/admin/billing-paddle-catalog/sync.
// Forces an EnsurePlanProducts round-trip (idempotent on Paddle-side
// products) and returns the post-sync catalog. The POST is gated by
// the idempotency middleware so a flaky-network retry replays the
// same 200 rather than re-issuing the SDK round-trip.
func (s *server) syncPaddleCatalog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	ops, ok := billingCatalogFor(s.billingProvider)
	if !ok {
		writeProviderNotImplemented(w, "sync", s.billingProvider)
		return
	}
	// ADR-093 / PR-D: budget-aware ceiling — childDeadline =
	// min(parentRemaining, billingOpsTimeout). When no budget is
	// attached (direct dashboard call without gatewayd-public
	// upstream), the legacy 30 s WithTimeout ceiling is preserved.
	ctx, cancel := budgetCtx(r.Context(), billingOpsTimeout)
	defer cancel()
	entries, err := ops.SyncBillingCatalog(ctx)
	if err != nil {
		// Wrap with the operation so an operator hitting the CLI sees
		// "paddle: sync catalog: <sdk-error>" rather than a bare string.
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_sync_failed",
			"Paddle catalog sync failed",
			err.Error()))
		return
	}
	if entries == nil {
		entries = []api.BillingCatalogEntry{}
	}
	writeJSON(w, http.StatusOK, paddleCatalogResponse{
		Provider: providerName(s.billingProvider),
		// SyncedAt is "now" by construction — SyncCatalog stamps
		// lastSyncAt via the EnsurePlanProducts path.
		SyncedAt: nowUTC().Format("2006-01-02T15:04:05Z07:00"),
		Entries:  entries,
	})
}

// resetPaddleCatalog handles DELETE /v1/admin/billing-paddle-catalog.
// Paddle's catalog is durable on the platform — the in-memory reset
// is a no-op (returns nil immediately) and the handler renders a
// 200 with empty entries so the CLI can print the "delete products
// from the Paddle Dashboard, then call sync" message.
//
// Future work (issue #279+) may add merchant-side cleanup; this
// handler will then return 502 on SDK failure rather than 200.
func (s *server) resetPaddleCatalog(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	ops, ok := billingCatalogFor(s.billingProvider)
	if !ok {
		writeProviderNotImplemented(w, "reset", s.billingProvider)
		return
	}
	if err := ops.ResetBillingCatalog(r.Context()); err != nil {
		if errors.Is(err, billing.ErrNotImplemented) {
			writeProviderNotImplemented(w, "reset", s.billingProvider)
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_reset_failed",
			"Billing catalog reset failed",
			err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, paddleCatalogResponse{
		Provider: providerName(s.billingProvider),
		SyncedAt: "",
		Entries:  []api.BillingCatalogEntry{},
	})
}

// paddleOveragePreflight handles GET
// /v1/admin/billing-paddle-overage/preflight (B4 / Tier 1
// follow-up to PR #802). Probes the paddle_overage_dedupe table
// for the four migration-00041 columns + per-state row counts
// and returns the snapshot as JSON. The CLI subcommand
// `faas billing reconcile-paddle-overage` is the only consumer;
// it maps each missing column to a clear "apply migration 00041"
// hint so an operator on a partially-applied DB doesn't see the
// failure as a generic meterd-loop 42703.
//
// The handler is always 200 — there is no error path the
// operator can fix from the box. A connection-level failure
// (Postgres down) bubbles up the standard 500 / 504 path. The
// pre-flight is intentionally cheap (single QueryRow for
// to_regclass + two SELECTs for columns + counts), so it can be
// safely called from CI / cron without rate-limiting concerns.
func (s *server) paddleOveragePreflight(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	if s.store == nil {
		// Same fail-closed posture as the reconcile handler when
		// billingProvider is nil: 501 with a stable code, not 500.
		// Pre-fix boxes that boot apid without a store (e.g. a
		// stateless admin tooling path) can still hit this route
		// and learn it's unreachable, rather than panicking.
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, "billing_unavailable",
			"State store not initialised",
			"apid booted without a state store; pre-flight is not reachable"))
		return
	}
	res, err := s.store.PaddleOverageDedupeSchema(r.Context())
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_preflight_failed",
			"Schema probe failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, api.BillingPaddleOveragePreflightResponse{
		TableExists:    res.TableExists,
		HasWindowStart: res.HasWindowStart,
		HasState:       res.HasState,
		HasClaimedAt:   res.HasClaimedAt,
		HasClaimedBy:   res.HasClaimedBy,
		PendingRows:    res.PendingRows,
		CompletedRows:  res.CompletedRows,
	})
}

// reconcileAccount handles POST /v1/admin/billing-reconcile/{id}.
// Loads the account, then calls s.billingProvider.ReconcileUsage.
// Stripe implements it (ADR-049 §B.1); Paddle returns
// billing.ErrNotImplemented (no usage-summary endpoint yet) and the
// handler maps that sentinel to 501.
//
// The reconcile writes back to state.Store via a future PR-C; this
// PR-P3 surfaces only the read-side so operators can validate the
// SDK round-trip before wiring the writeback. The reconciler
// goroutine in pkg/billing/reconciler is the consumer that will
// adopt this endpoint.
func (s *server) reconcileAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad account id", "expected UUID"))
		return
	}
	if s.billingProvider == nil {
		// No provider wired (Stripe pre-billingProvider boot path).
		// Return 501 with a clear "provider not initialised" rather
		// than a 500 — the absence is operator-config, not a bug.
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, "billing_unavailable",
			"Billing provider not initialised",
			"apid booted without a billing provider; reconcile is not reachable"))
		return
	}

	// Capability gate. Stripe's ReconcileUsage is a stub returning
	// ErrNotImplemented (pkg/billing/stripe/client.go) and Stripe's
	// Capabilities bitmask does NOT include CapUsageReconcile. Without
	// this check an operator on Stripe sees a misleading 501 citing
	// "ADR-049 §B.1" even though that capability is absent. Mirror the
	// reconciler's gate (pkg/billing/reconciler/reconciler.go).
	//
	// PR-P4 review finding #2: this gate MUST fire before
	// s.store.AccountByID — otherwise every Stripe reconcile hits
	// Postgres then 501s. Capability-gate before DB hit is the
	// standard ordering across all admin endpoints (see the gate
	// at handlers_admin.go for /v1/admin/users).
	if !s.billingProvider.Capabilities().Has(billing.CapUsageReconcile) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, "billing_reconcile_unsupported",
			"Billing provider does not support reconcile",
			providerName(s.billingProvider)+" does not advertise CapUsageReconcile; reconcile is unavailable on this provider"))
		return
	}
	target, err := s.store.AccountByID(r.Context(), targetID)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Account not found", err.Error()))
		return
	}
	// [start, end) window: rolling 30 days. The reconciler
	// goroutine reads usage_minutes for the same shape; aligning
	// the two means a future wireback is a copy-paste.
	now := nowUTC()
	start := now.AddDate(0, 0, -30)
	mbSeconds, err := s.billingProvider.ReconcileUsage(r.Context(), target, start, now)
	if err != nil {
		if errors.Is(err, billing.ErrNotImplemented) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, "billing_reconcile_unsupported",
				"Billing provider does not support reconcile",
				providerName(s.billingProvider)+" does not implement ReconcileUsage; see ADR-049 §B.1"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_reconcile_failed",
			"Reconcile failed", err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, api.BillingReconcileResponse{
		AccountID: target.ID,
		Start:     start,
		End:       now,
		MBSeconds: mbSeconds,
	})
}

// writeProviderNotImplemented renders a uniform 501 Problem for
// the case where the active provider does not implement the
// billing.CatalogProvider surface. Centralised so every handler renders
// the same shape.
func writeProviderNotImplemented(w http.ResponseWriter, op string, p billing.Provider) {
	name := providerName(p)
	if name == "" {
		name = "this provider"
	}
	api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, "billing_op_unsupported",
		"Billing provider does not support "+op+" catalog",
		name+" does not implement the billing catalog surface; this operation is provider-scoped"))
}
