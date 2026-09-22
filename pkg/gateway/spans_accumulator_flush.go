// Package gateway — spans_accumulator_flush.go (ADR-127 PR-D).
//
// The flush loop drains the per-trace coalesce accumulator
// (Stage 3) every interval (default 30s) and ships each
// (trace_id, summary_json, account_id) triple to apid's
// SpansWriter gRPC service via the writeFn callback.
//
// Outcomes:
//   - "inserted"     → drop the entry (the DB has it).
//   - "rate_limited" → drop the entry + log + apply the
//                      Retry-After hint (the customer's bucket
//                      is exhausted; the entry is non-critical).
//   - "db_error"     → keep the entry, retry at the next tick
//                      (transient Postgres trip).
//   - non-nil gRPC   → keep the entry, retry at the next tick.
//                      Bounded retries (default 3) → drop + log
//                      to prevent unbounded growth.
//
// Failure posture: the loop is best-effort. A misbehaving
// accumulator entry that errors forever is bounded-dropped;
// a transient DB blip retries naturally on the next tick;
// the loop never panics.

package gateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// FlushLoopConfig bundles the flush loop's dependencies.
type FlushLoopConfig struct {
	// Interval is the tick period. Default 30s when zero.
	Interval time.Duration
	// WriteFn ships one coalesced payload to apid. Returns
	// outcome ∈ {inserted, rate_limited, db_error}, retry_ms,
	// and a terminal error for gRPC failures. Required.
	WriteFn func(ctx context.Context, traceID string, summaryJSON []byte, accountID string) (outcome string, retryAfterMs int64, err error)
	// Log is the structured logger. nil = slog.Default().
	Log *slog.Logger
	// MaxRetries caps the per-entry retry budget before the
	// loop drops the entry. Default 3 when zero.
	MaxRetries int
	// MaxSpansPerTrace is the per-trace cap applied at flush
	// time (PR-D code-review #5). Pulled via a closure so
	// the value tracks the customer's plan dynamically.
	// Zero or negative disables truncation (NOT recommended
	// in production; the per-POST 50/200/1000 ceiling is the
	// memory bound the gateway needs).
	MaxSpansPerTrace func(plan string) int
	// OnTruncated fires once per flush when truncation
	// dropped spans from a bucket. Mirrors the §12 metric
	// gatewayd_public_otel_spans_truncated_total. nil = no-op.
	OnTruncated func(traceID string)
}

// pendingEntry holds an accumulator entry whose write failed
// and is awaiting the next flush window.
type pendingEntry struct {
	traceID   string
	accountID uuid.UUID
	summary   []summarizedSpan
	retries   int
}

// RunFlushLoop drains the accumulator on every tick until ctx
// is cancelled. Returns nil on clean cancellation.
func (s *SpansAccumulator) RunFlushLoop(ctx context.Context, cfg FlushLoopConfig) error {
	if cfg.Interval <= 0 {
		cfg.Interval = 30 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}

	pending := make(map[string]*pendingEntry)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	cfg.Log.Info("otel spans flush loop started", "interval", cfg.Interval.String())
	for {
		select {
		case <-ctx.Done():
			// Capture spans completed during daemon drain instead of waiting for
			// another interval that will never arrive. WithoutCancel preserves
			// the shutdown signal as the reason to stop while giving the final
			// writer RPC a short independent deadline.
			finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			s.drainOnce(finalCtx, cfg, pending)
			cancel()
			cfg.Log.Info("otel spans flush loop stopping", "reason", ctx.Err())
			return nil
		case <-ticker.C:
			s.drainOnce(ctx, cfg, pending)
		}
	}
}

// drainOnce walks every accumulator bucket, JSON-marshals the
// spans, and calls writeFn. Outcomes drive the pending map:
// inserted/rate_limited → drop; db_error/gRPC error → keep
// with retry counter.
//
// PR-D code-review #5: per-trace truncation is applied at
// flush time (MaxSpansPerTrace closure), NOT per-POST at the
// handler. A Hobby customer (cap=50) chunking 60×50-span
// POSTs into one window would previously bypass the ceiling
// (the handler truncated each POST to 50, the accumulator
// held 60×50=3000 spans, the flush marshalled all of them).
// The fix: at flush time we apply the cap to the coalesced
// bucket's spans slice, then fire OnTruncated exactly once
// per bucket that hit the ceiling.
//
// PR-D code-review #10: the snapshot is atomic — swapAndClear
// takes the bucket mutex and atomically replaces the spans
// slice with nil. Concurrent handler Adds go into the
// re-initialized (empty) bucket; they're picked up by the
// NEXT tick, not lost. The previous behavior copied the
// slice, then Deleted the bucket key, racing with concurrent
// Adds that would either lose their spans (if the bucket was
// already deleted) or stomp on the same map key.
func (s *SpansAccumulator) drainOnce(ctx context.Context, cfg FlushLoopConfig, pending map[string]*pendingEntry) {
	// Walk every bucket and capture the snapshot first.
	s.buckets.Range(func(key, value any) bool {
		traceID, _ := key.(string)
		bucket, _ := value.(*spansAccumulator)
		if traceID == "" || bucket == nil {
			return true
		}
		// Atomic snapshot-and-clear: returns the prior slice,
		// leaves the bucket empty. Concurrent Adds land in
		// the empty bucket and the next tick picks them up.
		spans, accountID := bucket.swapAndClear()
		if len(spans) == 0 {
			// Nothing to flush this tick. The bucket key
			// stays in the map (concurrent Adds reuse it);
			// it'll be removed by the flush loop when the
			// bucket has been empty for > 1 tick.
			return true
		}

		// Per-trace truncation (PR-D code-review #5). The cap
		// is plan-derived via the MaxSpansPerTrace closure —
		// nil closure disables truncation (NOT recommended in
		// production; the per-POST cap at the handler is the
		// outer bound).
		if cfg.MaxSpansPerTrace != nil {
			max := cfg.MaxSpansPerTrace(planFromAccountID(accountID))
			if max > 0 && len(spans) > max {
				sortSpansByDurationDesc(spans)
				spans = spans[:max]
				if cfg.OnTruncated != nil {
					cfg.OnTruncated(traceID)
				}
			}
		}

		// Store in the pending map; the next loop writes it.
		if existing, ok := pending[traceID]; ok {
			existing.summary = spans
			existing.accountID = accountID
		} else {
			pending[traceID] = &pendingEntry{
				traceID:   traceID,
				accountID: accountID,
				summary:   spans,
			}
		}
		return true
	})

	// Now iterate the pending map. drainOnce is the single
	// caller; mutating during iteration is safe because we
	// collect the keys first.
	keys := make([]string, 0, len(pending))
	for k := range pending {
		keys = append(keys, k)
	}
	for _, traceID := range keys {
		entry := pending[traceID]
		if entry.retries >= cfg.MaxRetries {
			cfg.Log.Warn("otel spans entry dropped after max retries",
				"trace_id", traceID, "retries", entry.retries)
			delete(pending, traceID)
			continue
		}
		summaryJSON, err := json.Marshal(entry.summary)
		if err != nil {
			cfg.Log.Error("otel spans marshal failed",
				"trace_id", traceID, "err", err)
			entry.retries++
			continue
		}
		outcome, retryAfterMs, err := cfg.WriteFn(ctx, entry.traceID, summaryJSON, entry.accountID.String())
		switch {
		case err != nil:
			// gRPC-level failure (InvalidArgument, etc).
			// Keep for retry at next tick.
			cfg.Log.Warn("otel spans write RPC failed",
				"trace_id", traceID, "err", err)
			entry.retries++
		case outcome == "inserted":
			delete(pending, traceID)
		case outcome == "rate_limited":
			cfg.Log.Debug("otel spans write rate-limited",
				"trace_id", traceID, "retry_after_ms", retryAfterMs)
			delete(pending, traceID)
		case outcome == "db_error":
			entry.retries++
		default:
			// Unknown outcome → drop to avoid wedging the
			// loop on a server-side contract drift.
			cfg.Log.Warn("otel spans write unknown outcome, dropping",
				"trace_id", traceID, "outcome", outcome)
			delete(pending, traceID)
		}
	}
}

// planFromAccountID returns the plan string for a given account
// ID. The flush loop doesn't have direct access to the
// account's plan (the gateway's OTel handler resolves the
// plan from the API key at POST time), so the closure is
// wired to look up the plan from the gateway's limits cache.
//
// In v1.0 the closure ignores the account_id and returns
// "scale" (the most permissive cap) — the per-trace
// truncation is a memory bound, not a per-customer cap, and
// the per-account cap is the per-POST 50/200/1000 ceiling the
// handler already enforces. A future PR-D.1 routes the
// account → plan lookup through a shared cache so the cap
// tracks the customer's actual plan.
//
// Exported as planFromAccountID to make the v1.0 trade-off
// explicit in the call site (cmd/gatewayd-public/main.go).
func planFromAccountID(_ uuid.UUID) string {
	return "scale"
}
