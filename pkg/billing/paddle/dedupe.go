package paddle

import (
	"context"
	"time"
)

// PaddleOverageDedupe is the cross-process claim state machine the
// meterd overage pusher drives before issuing a Paddle
// CreateTransaction POST. Per-window shape (account_id, window_start)
// — replaces the month-scoped pair that PR #204 shipped, which
// underbilled customers after the first positive window of the
// month because the meterd loop reads UsageByHour (window-scoped)
// but the dedupe row was keyed by calendarMonthStart (month-scoped).
// Migration 00037 added the per-window PK + state column to
// paddle_overage_dedupe.
//
// Flow:
//
//   - ClaimPaddleOverageWindow returns claimed=true only if this
//     caller now owns the (acct, window) and must proceed to POST.
//     Another pod holds the row otherwise (caller skips). Stale
//     pending claims older than the lease are reaped and re-claimed
//     transparently.
//   - CompletePaddleOverageWindow transitions the row to completed
//     after a successful SDK POST. Only the pod that holds the
//     claim is allowed to flip; foreign callers no-op because the
//     terminal state is already correct.
//   - ReapStalePaddleOverageClaims is called from meterd boot to
//     reset pending rows whose claim lease has expired, returning
//     them to the claimable pool. Idempotent.
//
// The deprecated month-scoped pair (HasPaddleOverageMonth /
// RecordPaddleOverageMonth) is retained on the state.Store
// interface for back-compat with PR #179 callers but is no longer
// used by meterd. Both state.MemStore and state.PgStore satisfy
// this interface via the same state.Store methods, so the meterd
// loader can pass `store` directly. Mirrors the PushDedupe shape
// in pkg/billing/stripe/client.go so the two providers share the
// same pattern at the same (per-hour) grain.
type PaddleOverageDedupe interface {
	// Deprecated month-scoped pair — kept for back-compat with
	// PR #179 callers. New code paths use the window-scoped pair
	// below.
	HasPaddleOverageMonth(ctx context.Context, accountID string, month time.Time) (bool, error)
	RecordPaddleOverageMonth(ctx context.Context, accountID string, month time.Time) error

	// PaddleOverageWindowExists is read before Claim so the provider can
	// distinguish a first delivery from recovery of an ambiguous prior POST.
	PaddleOverageWindowExists(ctx context.Context, accountID string, windowStart time.Time) (bool, error)
	ClaimPaddleOverageWindow(ctx context.Context, accountID string, windowStart time.Time, claimedBy string, lease time.Duration) (claimed bool, err error)
	CompletePaddleOverageWindow(ctx context.Context, accountID string, windowStart time.Time, mbSeconds int64) error
	ReapStalePaddleOverageClaims(ctx context.Context, olderThan time.Duration) (int, error)
}
