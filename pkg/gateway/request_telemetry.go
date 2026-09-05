// request_telemetry.go — the request-hot-path component of the
// production debugger (ADR-127).
//
// The gateway's Handler.observe exit funnel (handler.go:5456)
// calls Recorder.RecordFromObserve on every gateway-served
// request — unlike app_errors_recorder.go (which only fires on
// 4xx/5xx), every request lands here so the data plane can answer
// "did v81 make it slow?".
//
// Cardinality discipline is NOT the recorder's job in PR-A — the
// publisher (request_telemetry_publisher.go) is where the
// (app_id, deployment_id, route, status, minute) dedupe collapses
// burst traffic to a representative row + count. Doing it in the
// publisher keeps the recorder's hot path to O(1) under one mutex.
//
// The recorder NEVER opens a Postgres connection (CLAUDE.md
// ownership: apid is the sole writer). It hands rows to the
// publisher (request_telemetry_publisher.go) which dials apid via
// a unix-socket gRPC IncrementRequestTelemetry streaming RPC —
// same shape as app_errors_publisher.go.
//
// Concurrency: every method is safe under concurrent calls from
// many request goroutines. ringMu guards ring + head + len. The
// lock is held for O(1) work per row on the hot path (RecordFromObserve)
// and O(max) on the publisher drain path (DrainBatch).

package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// RequestTelemetryRow is the unit of work shared between recorder +
// publisher + apid receiver. Field names match the
// request_telemetry table columns in migrations/00427_ verbatim so
// the apid gRPC handler can pass them straight to sqlc params.
//
// PR-B adds Count — the collapse aggregate. The recorder always sets
// Count=1 (one observed request). The publisher's
// collapseRequestTelemetry increments Count when multiple rows fold
// into the same (app, deployment, route, method, status, minute)
// bucket. The apid receiver passes Count verbatim to the sqlc
// INSERT. Pre-PR-B clients (the recorder compiled against PR-A)
// never set Count; Go zero-value 0 is corrected to 1 at the
// recorder's enqueue boundary (RecordFromObserve) so a publisher
// collapse never has to reason about zero.
type RequestTelemetryRow struct {
	AccountID    uuid.UUID
	AppID        uuid.UUID
	DeploymentID uuid.UUID
	Route        string // route template (e.g. "GET /v1/users/{id}"), NOT expanded URL
	Method       string // closed enum: GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS
	Status       int    // 100-599 (CHECK constraint)
	LatencyMS    int    // wall-clock from Handler.ServeHTTP entry to observe exit
	ColdBoot     bool   // true when this request woke a fresh instance
	TraceID      string // W3C trace-id hex (32 chars); "" when unset
	ReceivedAt   time.Time
	Count        int // PR-B: collapse aggregate; >= 1 (CHECK in 00428). Recorder sets to 1.
}

// RequestTelemetryConfig bundles the knobs the recorder reads at
// boot. Defaults set via setDefaults.
type RequestTelemetryConfig struct {
	// Enabled is the kill-switch (FAAS_REQUEST_TELEMETRY_ENABLED).
	// When false, RecordFromObserve is a no-op and the publisher
	// goroutine does not start.
	Enabled bool

	// RingSize caps the in-process ringbuffer. Past the cap,
	// the oldest row is overwritten (next publisher tick drops
	// further rows if the channel stays full). Sized to absorb
	// a 1k-RPS app at 1s flush cadence (4096 = 4× headroom).
	RingSize int

	// Now is injectable for tests. nil ⇒ time.Now.
	Now func() time.Time
}

func (c *RequestTelemetryConfig) setDefaults() {
	if c.RingSize == 0 {
		c.RingSize = 4096
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// requestTelemetryRecorder is the in-process hot-path component.
// Construct via NewRequestTelemetryRecorder and have the publisher
// call DrainBatch on its tick (5s default per
// app_errors_publisher.go:50). Handler.observe calls
// RecordFromObserve to enqueue rows.
type requestTelemetryRecorder struct {
	cfg RequestTelemetryConfig
	log *slog.Logger

	// ringMu guards ring + head + len. The recorder holds the
	// lock for O(1) work per row; the publisher holds it for the
	// duration of DrainBatch (typically < 1ms for a 256-row
	// batch).
	ringMu sync.Mutex
	ring   []RequestTelemetryRow
	head   int
	len    int
}

// NewRequestTelemetryRecorder wires the recorder. The publisher
// obtains a reference to this struct and calls DrainBatch on its
// tick (see request_telemetry_publisher.go).
func NewRequestTelemetryRecorder(cfg RequestTelemetryConfig, log *slog.Logger) *requestTelemetryRecorder {
	cfg.setDefaults()
	return &requestTelemetryRecorder{
		cfg:  cfg,
		log:  log,
		ring: make([]RequestTelemetryRow, cfg.RingSize),
	}
}

// RecordFromObserve is the seam Handler.observe uses to enqueue
// a row at the gateway's single exit funnel (handler.go:5456).
// The caller (observe) has already resolved the status + elapsed +
// cold + target from its arguments, so it passes the row in
// pre-built rather than reading from request context.
//
// Safe under concurrent calls — goes through enqueue() under
// ringMu, same path as any future publisher-side append.
//
// PR-B: the recorder always sets Count=1 at this boundary. Pre-PR-B
// callers leave Count=0; without the explicit assignment the
// collapse aggregate would propagate a zero into the apid INSERT
// and trip the count >= 1 CHECK. (Collapsed rows have Count >= 2
// by definition; a Count=1 row is "no collapse happened".)
func (r *requestTelemetryRecorder) RecordFromObserve(row RequestTelemetryRow) {
	if !r.cfg.Enabled {
		return
	}
	if row.Count < 1 {
		row.Count = 1
	}
	r.enqueue(row)
}

// enqueue is the single entry point for the ringbuffer append.
// O(1) under ringMu. Burst traffic overwrites the oldest row
// past the cap — the publisher's drain is the back-pressure
// signal (it should drain faster than the ring fills).
func (r *requestTelemetryRecorder) enqueue(row RequestTelemetryRow) {
	r.ringMu.Lock()
	if r.len < len(r.ring) {
		// ring not full — append at tail
		tail := (r.head + r.len) % len(r.ring)
		r.ring[tail] = row
		r.len++
	} else {
		// ring full — overwrite head, advance head by 1
		r.ring[r.head] = row
		r.head = (r.head + 1) % len(r.ring)
	}
	r.ringMu.Unlock()
}

// DrainBatch returns up to max rows from the head of the ringbuffer
// and removes them. Returns an empty slice when the ring is empty.
// The publisher calls this on its tick (5s default) and ships the
// batch to apid via gRPC.
func (r *requestTelemetryRecorder) DrainBatch(max int) []RequestTelemetryRow {
	if max <= 0 {
		return nil
	}
	r.ringMu.Lock()
	defer r.ringMu.Unlock()
	if r.len == 0 {
		return nil
	}
	n := r.len
	if n > max {
		n = max
	}
	out := make([]RequestTelemetryRow, n)
	for i := 0; i < n; i++ {
		out[i] = r.ring[(r.head+i)%len(r.ring)]
	}
	// Advance head by n, shrink len by n. The ring slot positions
	// stay allocated (no realloc) — the publisher will overwrite
	// them on the next enqueue.
	r.head = (r.head + n) % len(r.ring)
	r.len -= n
	return out
}

// PendingCount returns the number of rows currently in the
// ringbuffer waiting for the publisher. Read-only; useful for
// /metrics + tests.
func (r *requestTelemetryRecorder) PendingCount() int {
	r.ringMu.Lock()
	defer r.ringMu.Unlock()
	return r.len
}

// RingCapacity returns the configured ringbuffer size. Read-only;
// useful for /metrics + tests.
func (r *requestTelemetryRecorder) RingCapacity() int {
	return len(r.ring)
}

// --- context-key helpers for the ServeHTTP-side stamping ---

// accountIDContextKey / appIDContextKey are the typed context keys
// that ServeHTTP uses (via withAppAndAccount below) to thread the
// resolved account_id + app_id from the haveApp: label to
// Handler.observe. Both values are uuid.UUID; the zero UUID is the
// "not stamped" sentinel.
type (
	accountIDContextKey struct{}
	appIDContextKey     struct{}
)

// withAppAndAccount stamps account_id + app_id onto r's context.
// Called once per request from Handler.ServeHTTP at the haveApp:
// label (handler.go:4620) so observe can read both via
// accountIDFromContext / appIDFromContext.
//
// Returns the original request unchanged when either id is the
// zero UUID — pre-picker paths (auth failure, no host) skip the
// stamp so observe sees an absent key and drops the row silently
// (the same pre-picker posture app_errors_recorder.go uses).
func withAppAndAccount(r *http.Request, accountID, appID uuid.UUID) *http.Request {
	if r == nil {
		return r
	}
	if accountID == uuid.Nil || appID == uuid.Nil {
		return r
	}
	ctx := context.WithValue(r.Context(), accountIDContextKey{}, accountID)
	ctx = context.WithValue(ctx, appIDContextKey{}, appID)
	return r.WithContext(ctx)
}

// accountIDFromContext reads the account_id stamped on ctx.
// Returns uuid.Nil when absent (pre-picker paths).
func accountIDFromContext(ctx context.Context) uuid.UUID {
	v, _ := reqContextUUID(ctx, accountIDContextKey{})
	return v
}

// appIDFromContext reads the app_id stamped on ctx.
// Returns uuid.Nil when absent (pre-picker paths).
func appIDFromContext(ctx context.Context) uuid.UUID {
	v, _ := reqContextUUID(ctx, appIDContextKey{})
	return v
}

// reqContextUUID reads a uuid.UUID from ctx by the given key.
// Returns uuid.Nil + false when absent.
func reqContextUUID(ctx context.Context, key any) (uuid.UUID, bool) {
	v, ok := ctx.Value(key).(uuid.UUID)
	return v, ok
}

// A caller-supplied request ID need not be a W3C trace ID. Preserve the
// request record while omitting an incompatible optional trace reference.
func telemetryTraceID(requestID string) string {
	if len(requestID) != 32 {
		return ""
	}
	for _, c := range requestID {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return requestID
}
