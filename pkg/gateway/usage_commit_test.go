package gateway

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// usageProbe records, at each write that reaches the client, how many usage
// events the outbox held.
type usageProbe struct {
	header        http.Header
	q             *usageoutbox.Outbox
	status        int
	body          []byte
	pendingAtLast int64
	flushedAt     int64
}

func newUsageProbe(q *usageoutbox.Outbox) *usageProbe {
	return &usageProbe{header: http.Header{}, q: q, pendingAtLast: -1, flushedAt: -1}
}

func (p *usageProbe) Header() http.Header { return p.header }
func (p *usageProbe) WriteHeader(code int) {
	if p.status == 0 {
		p.status = code
	}
}
func (p *usageProbe) Write(b []byte) (int, error) {
	p.pendingAtLast = p.q.Stats().PendingRecords
	p.body = append(p.body, b...)
	return len(b), nil
}
func (p *usageProbe) Flush() { p.flushedAt = p.q.Stats().PendingRecords }

type usageCommitFixture struct {
	h      *Handler
	q      *usageoutbox.Outbox
	r      *http.Request
	rec    *statusRecorder
	probe  *usageProbe
	holder *usageCommitHolder
}

// newUsageCommitFixture arms a consumer request whose guest responded.
// maxBytes 1 makes every outbox append fail.
func newUsageCommitFixture(t *testing.T, method string, maxBytes int64, consumer bool) usageCommitFixture {
	t.Helper()
	q, err := usageoutbox.Open(t.TempDir(), maxBytes)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{usageOutbox: q, apiMonetization: monetizationOn, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	acct, app := uuid.New(), uuid.New()
	r := httptest.NewRequest(method, "/items", nil)
	r = withAppAndAccount(r, acct, app)
	if consumer {
		id := uuid.NewString()
		r = r.WithContext(authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{ID: id, AppID: app.String()}))
	}
	guest := &firstByteRecorder{}
	guest.record(time.Now())
	r = r.WithContext(WithFirstByteRecorder(r.Context(), guest))
	holder := &usageCommitHolder{}
	r = r.WithContext(withUsageCommitHolder(r.Context(), holder))
	h.armUsageCommit(r, Target{})
	probe := newUsageProbe(q)
	rec := &statusRecorder{ResponseWriter: probe, status: http.StatusOK, request: r, usage: holder}
	return usageCommitFixture{h: h, q: q, r: r, rec: rec, probe: probe, holder: holder}
}

func abortPanics(holder *usageCommitHolder, rec *statusRecorder) (aborted bool) {
	defer func() {
		if p := recover(); p != nil {
			err, ok := p.(error)
			aborted = ok && errors.Is(err, http.ErrAbortHandler)
		}
	}()
	abortUnrecordedResponse(holder, rec)
	return false
}

// adr: 1505 — the bytes completing a declared-length response reach the
// client only after the usage event is durable, and observe never appends
// it a second time.
func TestUsageCommitPrecedesCompletingWrite(t *testing.T) {
	f := newUsageCommitFixture(t, http.MethodGet, 4096, true)
	if f.holder.armed() == nil {
		t.Fatal("consumer request was not armed")
	}
	f.rec.Header().Set("Content-Length", "10")
	f.rec.WriteHeader(http.StatusOK)
	if _, err := f.rec.Write([]byte("hello")); err != nil || f.probe.pendingAtLast != 0 {
		t.Fatalf("first half: err=%v pending=%d, want the event not yet appended", err, f.probe.pendingAtLast)
	}
	if _, err := f.rec.Write([]byte("world")); err != nil || f.probe.pendingAtLast != 1 {
		t.Fatalf("completing write: err=%v pending=%d, want the event durable first", err, f.probe.pendingAtLast)
	}
	f.h.observe(f.r, http.StatusOK, "app", string(api.PlanPro), false, Target{})
	if aborted := abortPanics(f.holder, f.rec); aborted {
		t.Fatal("recorded response aborted")
	}
	if got := f.q.Stats().PendingRecords; got != 1 {
		t.Fatalf("pending events = %d, want exactly one", got)
	}
	item, ok, err := f.q.Next()
	if err != nil || !ok || item.Event.EventID != f.holder.armed().eventID.String() || item.Event.BillableUnits != 1 || item.Event.ConsumerID == "" {
		t.Fatalf("usage event = %+v, ok=%t err=%v", item.Event, ok, err)
	}
}

// adr: 1505 — without durable usage, a billable response never completes:
// the completing write is refused and the handler exit aborts.
func TestUsageCommitWithholdsUnrecordedResponse(t *testing.T) {
	f := newUsageCommitFixture(t, http.MethodGet, 1, true)
	f.rec.Header().Set("Content-Length", "4")
	f.rec.WriteHeader(http.StatusOK)
	if n, err := f.rec.Write([]byte("body")); n != 0 || !errors.Is(err, errUsageNotDurable) || len(f.probe.body) != 0 {
		t.Fatalf("write = %d, %v, body %q; want withheld", n, err, f.probe.body)
	}
	if !abortPanics(f.holder, f.rec) {
		t.Fatal("handler exit did not abort the response")
	}

	upgrade := newUsageCommitFixture(t, http.MethodGet, 1, true)
	upgrade.rec.WriteHeader(http.StatusSwitchingProtocols)
	if upgrade.probe.status != http.StatusServiceUnavailable {
		t.Fatalf("upgrade status = %d, want 503 instead of a session", upgrade.probe.status)
	}
}

// adr: 1505 — a response net/http completes after the handler returns (no
// body, or chunked) commits at exit, and before any flush that would send it.
func TestUsageCommitAtExitAndBeforeFlush(t *testing.T) {
	f := newUsageCommitFixture(t, http.MethodGet, 4096, true)
	f.rec.installFlushHook(f.probe, func(int64) {}, 1<<20, time.Hour, 0)
	f.rec.WriteHeader(http.StatusNoContent)
	if f.q.Stats().PendingRecords != 0 {
		t.Fatal("204 committed before it could complete")
	}
	f.rec.Flush()
	if f.probe.flushedAt != 1 {
		t.Fatalf("flush saw %d pending events, want the 204's usage first", f.probe.flushedAt)
	}

	chunked := newUsageCommitFixture(t, http.MethodGet, 4096, true)
	chunked.rec.WriteHeader(http.StatusOK)
	if _, err := chunked.rec.Write([]byte("partial")); err != nil || chunked.q.Stats().PendingRecords != 0 {
		t.Fatalf("chunked body committed early: err=%v", err)
	}
	if abortPanics(chunked.holder, chunked.rec) || chunked.q.Stats().PendingRecords != 1 {
		t.Fatalf("exit commit: pending=%d", chunked.q.Stats().PendingRecords)
	}
}

// adr: 1505 — anonymous traffic is not armed, and an unbillable platform
// failure whose usage cannot be recorded is not aborted.
func TestUsageCommitOnlyGuardsBillableResponses(t *testing.T) {
	anonymous := newUsageCommitFixture(t, http.MethodGet, 4096, false)
	if anonymous.holder.armed() != nil {
		t.Fatal("anonymous request armed a usage commit")
	}
	q, err := usageoutbox.Open(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{usageOutbox: q, apiMonetization: monetizationOn, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/items", nil), uuid.New(), uuid.New())
	r = r.WithContext(authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{ID: uuid.NewString()}))
	holder := &usageCommitHolder{}
	r = r.WithContext(withUsageCommitHolder(r.Context(), holder))
	h.armUsageCommit(r, Target{})
	rec := &statusRecorder{ResponseWriter: newUsageProbe(q), status: http.StatusBadGateway, request: r, usage: holder}
	if abortPanics(holder, rec) {
		t.Fatal("unbillable platform failure was aborted")
	}
}
