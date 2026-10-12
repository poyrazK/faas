// AdvisoryReceiver unit tests — Wave 0 PR-C / ADR-047.
//
// These tests exercise the in-package advisoryReceiver via the
// apidpb.AdvisoryServer gRPC surface, no real Postgres. The store,
// audit, notifier, and logger are all stubbed. Pins:
//   - happy path: app row exists, audit.Emit called once with the
//     right (kind, subject, data) tuple; pg_notify fires once.
//   - missing app row: codes.NotFound (not codes.Internal —
//     distinguishes "app gone" from "transient DB blip" for vmmd's
//     retry logic).
//   - missing fields: codes.InvalidArgument for both app_id and
//     instance.
//   - store error other than NotFound: subject=NULL fallback (the
//     audit row is still emitted), so a transient Postgres blip
//     doesn't drop the row.
//   - subject=NULL path: when app.AccountID is empty, no subject
//     pointer; the audit row's subject column is NULL.
//   - notify nil-tolerance: a nil notifier doesn't crash the receiver
//     (default-local posture).

package apidserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- test stubs -------------------------------------------------------------

// advisoryStubStore satisfies advisoryStore. Configurable per-test via
// app field. Prefixed `advisory` to avoid colliding with the
// `stubStore` / `stubNotifier` defined in server_test.go for the
// Move 2 long-poll suite.
type advisoryStubStore struct {
	app state.App
	err error
}

func (s *advisoryStubStore) AppByID(_ context.Context, _ string) (state.App, error) {
	return s.app, s.err
}

// advisoryStubAudit satisfies auditEmitter. Records every Emit call.
type advisoryStubAudit struct {
	mu    sync.Mutex
	calls []advisoryStubAuditCall
}

type advisoryStubAuditCall struct {
	Kind    string
	Subject *string
	Data    map[string]any
}

func (s *advisoryStubAudit) Emit(_ context.Context, kind string, subject *string, data map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Defensive copy of data so test mutations don't bleed.
	copied := make(map[string]any, len(data))
	for k, v := range data {
		copied[k] = v
	}
	s.calls = append(s.calls, advisoryStubAuditCall{Kind: kind, Subject: subject, Data: copied})
}

func (s *advisoryStubAudit) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// advisoryStubNotifier satisfies Notifier. Records every Notify call.
type advisoryStubNotifier struct {
	mu      sync.Mutex
	calls   []advisoryStubNotifyCall
	failErr error // non-nil → Notify returns this
}

type advisoryStubNotifyCall struct {
	Channel string
	Payload string
}

func (s *advisoryStubNotifier) Notify(_ context.Context, channel, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, advisoryStubNotifyCall{Channel: channel, Payload: payload})
	return s.failErr
}

func (s *advisoryStubNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}

func (s *advisoryStubNotifier) WaitFor(_ context.Context, _ string, _ func(string) bool, _ time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

// discardLog returns a slog.Logger that swallows every line.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newReceiver wires a receiver with all four stubs. Any nil arg is
// preserved as nil so the test can exercise nil-tolerance branches.
func newAdvisoryReceiver(store advisoryStore, audit auditEmitter, notif Notifier) *advisoryReceiver {
	return &advisoryReceiver{
		store:  store,
		audit:  audit,
		notif:  notif,
		logger: discardLog(),
	}
}

// --- tests ------------------------------------------------------------------

// TestAdvisoryReceiver_DispatchesToAuditor pins the happy path:
// one audit row written with the right kind, subject, and data
// shape; one pg_notify fired on the new channel.
func TestAdvisoryReceiver_DispatchesToAuditor(t *testing.T) {
	acct := "acct-1"
	store := &advisoryStubStore{app: state.App{AccountID: acct}}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{
		Instance: "i-1",
		AppId:    "a-1",
		Events: []*apidpb.AdvisoryEvent{
			{Path: "/data/foo", Mask: []string{"create", "modify"}, Pid: 42, TsUnixMs: 1700000000000},
			{Path: "/data/bar", Mask: []string{"move"}, Pid: 42, TsUnixMs: 1700000001000},
		},
	}
	resp, err := rcv.ForwardStatelessAdvisory(context.Background(), req)
	if err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v", err)
	}
	if resp == nil {
		t.Fatal("response is nil")
	}

	if got := audit.callCount(); got != 1 {
		t.Fatalf("audit Emit count = %d, want 1", got)
	}
	call := audit.calls[0]
	if call.Kind != "stateless.advisory" {
		t.Errorf("kind = %q, want stateless.advisory", call.Kind)
	}
	if call.Subject == nil || *call.Subject != acct {
		t.Errorf("subject = %v, want pointer to %q", call.Subject, acct)
	}
	if got := call.Data["instance"]; got != "i-1" {
		t.Errorf("data.instance = %v, want i-1", got)
	}
	if got := call.Data["app_id"]; got != "a-1" {
		t.Errorf("data.app_id = %v, want a-1", got)
	}
	if got := call.Data["count"]; got != 2 {
		t.Errorf("data.count = %v, want 2", got)
	}
	events, ok := call.Data["events"].([]map[string]any)
	if !ok {
		t.Fatalf("data.events type = %T, want []map[string]any", call.Data["events"])
	}
	if len(events) != 2 {
		t.Fatalf("data.events len = %d, want 2", len(events))
	}
	if events[0]["path"] != "/data/foo" {
		t.Errorf("data.events[0].path = %v, want /data/foo", events[0]["path"])
	}
	if events[0]["mask"] != "create,modify" {
		t.Errorf("data.events[0].mask = %v, want create,modify", events[0]["mask"])
	}

	// pg_notify fires once on the new channel.
	if got := len(notif.calls); got != 1 {
		t.Fatalf("notifier Notify count = %d, want 1", got)
	}
	if notif.calls[0].Channel != db.NotifyStatelessAdvisory {
		t.Errorf("notify channel = %q, want %q", notif.calls[0].Channel, db.NotifyStatelessAdvisory)
	}
	// Payload should contain app_id, instance, n, sample_path.
	if p := notif.calls[0].Payload; p == "" || !strings.Contains(p, `"app_id":"a-1"`) {
		t.Errorf("notify payload = %q, missing app_id field", p)
	}
}

// TestAdvisoryReceiver_AppNotFoundMapsToNotFound pins the NotFound
// branch — mirrors the vmmd gRPC convention at
// pkg/vmmdgrpc/server.go:67-72. vmmd's retry logic distinguishes
// "app gone" (NotFound, do not retry) from "transient DB blip"
// (other errors, OK to retry).
func TestAdvisoryReceiver_AppNotFoundMapsToNotFound(t *testing.T) {
	store := &advisoryStubStore{err: state.ErrNotFound}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a-missing", Events: []*apidpb.AdvisoryEvent{{Path: "/x"}}}
	_, err := rcv.ForwardStatelessAdvisory(context.Background(), req)
	if err == nil {
		t.Fatal("expected NotFound, got nil")
	}
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound", code)
	}
	if msg := status.Convert(err).Message(); !strings.Contains(msg, "a-missing") {
		t.Errorf("message = %q, want it to name the missing app_id", msg)
	}
	// No audit row written, no pg_notify fired.
	if got := audit.callCount(); got != 0 {
		t.Errorf("audit Emit count = %d, want 0 (NotFound short-circuits)", got)
	}
	if got := len(notif.calls); got != 0 {
		t.Errorf("notifier Notify count = %d, want 0", got)
	}
}

// TestAdvisoryReceiver_AppNotFoundByPgxErr pins the defensive
// substring path: pgx wraps no-rows as pgx.ErrNoRows; the receiver
// must still recognise it as NotFound rather than mapping to
// Internal. (state.PgStore converts in production; a stub that
// returns the bare pgx error must still trip the branch.)
func TestAdvisoryReceiver_AppNotFoundByPgxErr(t *testing.T) {
	store := &advisoryStubStore{err: errors.New("no rows in result set")}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a-missing", Events: []*apidpb.AdvisoryEvent{{Path: "/x"}}}
	_, err := rcv.ForwardStatelessAdvisory(context.Background(), req)
	if code := status.Code(err); code != codes.NotFound {
		t.Errorf("code = %v, want NotFound (sub-string branch)", code)
	}
}

// TestAdvisoryReceiver_OtherStoreError_EmitsWithoutSubject pins the
// fallback path: a transient Postgres blip (any non-NotFound error)
// must NOT drop the row — the audit Emit is still called with
// subject=NULL so the row lands in the table and is surfaced via the
// include_anonymous query param.
func TestAdvisoryReceiver_OtherStoreError_EmitsWithoutSubject(t *testing.T) {
	store := &advisoryStubStore{err: errors.New("connection reset by peer")}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a", Events: []*apidpb.AdvisoryEvent{{Path: "/data/x"}}}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v (other-store-error must not bubble)", err)
	}
	if got := audit.callCount(); got != 1 {
		t.Fatalf("audit Emit count = %d, want 1", got)
	}
	if call := audit.calls[0]; call.Subject != nil {
		t.Errorf("subject = %v, want nil (anonymous fallback)", call.Subject)
	}
	if got := len(notif.calls); got != 1 {
		t.Errorf("notifier Notify count = %d, want 1", got)
	}
}

// TestAdvisoryReceiver_AppHasEmptyAccountID_EmitsWithoutSubject
// covers the rare defensive case: app row exists but its account_id
// is empty (data drift, e.g. before the G2 seed migration). The
// audit row's subject column must be NULL.
func TestAdvisoryReceiver_AppHasEmptyAccountID_EmitsWithoutSubject(t *testing.T) {
	store := &advisoryStubStore{app: state.App{AccountID: ""}}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a", Events: []*apidpb.AdvisoryEvent{{Path: "/data/x"}}}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v", err)
	}
	if got := audit.callCount(); got != 1 {
		t.Fatalf("audit Emit count = %d, want 1", got)
	}
	if call := audit.calls[0]; call.Subject != nil {
		t.Errorf("subject = %v, want nil", call.Subject)
	}
}

// TestAdvisoryReceiver_NilStore_EmitsWithoutSubject covers the
// nil-store posture (default-local dev). Subject falls through to
// nil; the audit row is still written.
func TestAdvisoryReceiver_NilStore_EmitsWithoutSubject(t *testing.T) {
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(nil, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a", Events: []*apidpb.AdvisoryEvent{{Path: "/data/x"}}}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v", err)
	}
	if got := audit.callCount(); got != 1 {
		t.Fatalf("audit Emit count = %d, want 1 (nil store must still emit)", got)
	}
}

// TestAdvisoryReceiver_NilAuditAndNotifier_NoCrash pins the
// fully-unwired posture: when the receiver is constructed with nil
// store, nil audit, nil notifier (the cmd/apid default-local code
// path), ForwardStatelessAdvisory must not panic.
func TestAdvisoryReceiver_NilAuditAndNotifier_NoCrash(t *testing.T) {
	rcv := newAdvisoryReceiver(nil, nil, nil)
	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a", Events: []*apidpb.AdvisoryEvent{{Path: "/data/x"}}}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v", err)
	}
}

// TestAdvisoryReceiver_NilRequest_InvalidArgument pins the nil-req
// branch — matches the "missing field" pattern in the gRPC handler
// convention.
func TestAdvisoryReceiver_NilRequest_InvalidArgument(t *testing.T) {
	rcv := newAdvisoryReceiver(&advisoryStubStore{}, &advisoryStubAudit{}, &advisoryStubNotifier{})
	_, err := rcv.ForwardStatelessAdvisory(context.Background(), nil)
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestAdvisoryReceiver_MissingAppID_InvalidArgument pins the
// missing-app_id branch.
func TestAdvisoryReceiver_MissingAppID_InvalidArgument(t *testing.T) {
	rcv := newAdvisoryReceiver(&advisoryStubStore{}, &advisoryStubAudit{}, &advisoryStubNotifier{})
	_, err := rcv.ForwardStatelessAdvisory(context.Background(), &apidpb.ForwardStatelessAdvisoryRequest{
		Instance: "i",
		Events:   []*apidpb.AdvisoryEvent{{Path: "/data/x"}},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestAdvisoryReceiver_MissingInstance_InvalidArgument pins the
// missing-instance branch.
func TestAdvisoryReceiver_MissingInstance_InvalidArgument(t *testing.T) {
	rcv := newAdvisoryReceiver(&advisoryStubStore{}, &advisoryStubAudit{}, &advisoryStubNotifier{})
	_, err := rcv.ForwardStatelessAdvisory(context.Background(), &apidpb.ForwardStatelessAdvisoryRequest{
		AppId:  "a",
		Events: []*apidpb.AdvisoryEvent{{Path: "/data/x"}},
	})
	if code := status.Code(err); code != codes.InvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", code)
	}
}

// TestAdvisoryReceiver_NotifierErrorSwallowed pins ADR-035: a
// dropped pg_notify means the dashboard SSE misses this frame, but
// the audit row at /v1/audit-events is still there. The receiver
// must not bubble the notifier error.
func TestAdvisoryReceiver_NotifierErrorSwallowed(t *testing.T) {
	store := &advisoryStubStore{app: state.App{AccountID: "acct"}}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{failErr: errors.New("pg_notify: connection refused")}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{Instance: "i", AppId: "a", Events: []*apidpb.AdvisoryEvent{{Path: "/data/x"}}}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v (notifier errors must be swallowed)", err)
	}
	// Audit row still written.
	if got := audit.callCount(); got != 1 {
		t.Errorf("audit Emit count = %d, want 1 (notifier failure must not drop audit row)", got)
	}
}

// TestAdvisoryReceiver_SamplePathFromFirstEvent pins the SSE summary
// payload's sample_path field. The first non-nil event's path is
// shipped so the dashboard can hint at which path triggered the
// advisory without round-tripping to /v1/audit-events.
func TestAdvisoryReceiver_SamplePathFromFirstEvent(t *testing.T) {
	store := &advisoryStubStore{app: state.App{AccountID: "acct"}}
	audit := &advisoryStubAudit{}
	notif := &advisoryStubNotifier{}
	rcv := newAdvisoryReceiver(store, audit, notif)

	req := &apidpb.ForwardStatelessAdvisoryRequest{
		Instance: "i",
		AppId:    "a",
		Events: []*apidpb.AdvisoryEvent{
			{Path: "/data/first", Mask: []string{"create"}},
			{Path: "/data/second", Mask: []string{"modify"}},
		},
	}
	if _, err := rcv.ForwardStatelessAdvisory(context.Background(), req); err != nil {
		t.Fatalf("ForwardStatelessAdvisory: %v", err)
	}
	if got := len(notif.calls); got != 1 {
		t.Fatalf("notifier calls = %d, want 1", got)
	}
	p := notif.calls[0].Payload
	if !strings.Contains(p, `"sample_path":"/data/first"`) {
		t.Errorf("payload = %q, want sample_path = /data/first", p)
	}
	if !strings.Contains(p, `"n":2`) {
		t.Errorf("payload = %q, want n = 2", p)
	}
}

// --- Move 1 PR-A: severity classification ---------------------------------

// TestSeverityForPath_PinClassification pins the closed-path severity
// table so a future path addition (or a path-list drift between
// guest-init, dashboard, and the apid receiver) is caught at test
// time. The four cases below mirror pkg/dashboard/dashboard.go's
// StatelessClosedPaths severity field — keeping both lists in sync
// is the lockstep the dashboard tests pin on the other side.
func TestSeverityForPath_PinClassification(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"/data", "high"},
		{"/db", "high"},
		{"/var/lib/postgresql", "high"},
		{"/var/lib/mysql", "high"},
		{"/var/lib/redis", "warn"},
		{"/var/lib/mongodb", "warn"},
		{"/var/lib/cockroach", "warn"},
		{"/var/lib/cassandra", "warn"},
		{"/var/lib/clickhouse", "warn"},
		{"/tmp/foo", "warn"}, // unwatched paths still classify as warn (defensive)
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			if got := severityForPath(c.path); got != c.want {
				t.Errorf("severityForPath(%q) = %q, want %q", c.path, got, c.want)
			}
		})
	}
}

// TestAdvisoryBatchSeverity_EmptyBatchIsInfo: an empty events list
// returns "info" (defensive default — the audit row's severity
// field is always populated so the dashboard's badge column
// never renders an empty cell).
func TestAdvisoryBatchSeverity_EmptyBatchIsInfo(t *testing.T) {
	if got := advisoryBatchSeverity(nil); got != "info" {
		t.Errorf("advisoryBatchSeverity(nil) = %q, want info", got)
	}
	if got := advisoryBatchSeverity([]*apidpb.AdvisoryEvent{}); got != "info" {
		t.Errorf("advisoryBatchSeverity([]) = %q, want info", got)
	}
}

// TestAdvisoryBatchSeverity_HighestWins: a batch containing one
// "high" path returns "high" even when other events are "warn".
// This is the triaging signal Move 1 PR-A's dashboard badge column
// relies on — a single /data write in a 100-event batch is still
// "high" for the row.
func TestAdvisoryBatchSeverity_HighestWins(t *testing.T) {
	cases := []struct {
		name   string
		events []*apidpb.AdvisoryEvent
		want   string
	}{
		{
			name: "all warn",
			events: []*apidpb.AdvisoryEvent{
				{Path: "/var/lib/redis"},
				{Path: "/var/lib/mongodb"},
			},
			want: "warn",
		},
		{
			name: "one high + many warn",
			events: []*apidpb.AdvisoryEvent{
				{Path: "/var/lib/redis"},
				{Path: "/data/first"},
				{Path: "/var/lib/cockroach"},
			},
			want: "high",
		},
		{
			name: "only high",
			events: []*apidpb.AdvisoryEvent{
				{Path: "/db"},
			},
			want: "high",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := advisoryBatchSeverity(c.events); got != c.want {
				t.Errorf("advisoryBatchSeverity(%v) = %q, want %q", c.events, got, c.want)
			}
		})
	}
}

// TestAdvisoryReceiver_DataIncludesSeverityField: end-to-end check
// that the emitted audit row's data map carries the "severity"
// key. The dashboard's --verbose renderer reads this field; if
// it goes missing the badge column drops to "info" silently.
func TestAdvisoryReceiver_DataIncludesSeverityField(t *testing.T) {
	acct := "acct-1"
	store := &advisoryStubStore{app: state.App{ID: "app-uuid", AccountID: acct, Slug: "test"}}
	audit := &advisoryStubAudit{}
	recv := newAdvisoryReceiver(store, audit, nil)

	_, err := recv.ForwardStatelessAdvisory(context.Background(), &apidpb.ForwardStatelessAdvisoryRequest{
		AppId: "app-uuid", Instance: "i-1",
		Events: []*apidpb.AdvisoryEvent{
			{Path: "/data/foo", Mask: []string{"create"}},
		},
	})
	if err != nil {
		t.Fatalf("forward: %v", err)
	}
	if audit.callCount() != 1 {
		t.Fatalf("audit calls = %d, want 1", audit.callCount())
	}
	call := audit.calls[0]
	got, _ := call.Data["severity"].(string)
	if got != "high" {
		t.Errorf("data.severity = %q, want high (path /data/foo is high)", got)
	}
}
