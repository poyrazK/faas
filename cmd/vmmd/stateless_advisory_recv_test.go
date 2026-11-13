package main

// adr: 047

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

type advisoryForwardCall struct {
	instance, appID string
	events          []fcvm.AdvisoryEvent
}

type advisoryForwardRecorder struct {
	mu    sync.Mutex
	calls []advisoryForwardCall
}

func (r *advisoryForwardRecorder) forward(_ context.Context, instance, appID string, events []fcvm.AdvisoryEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, advisoryForwardCall{instance: instance, appID: appID, events: events})
	return nil
}

func advisoryFrame(t *testing.T, msgType uint32, body any) []byte {
	t.Helper()
	raw, ok := body.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatal(err)
		}
	}
	frame := make([]byte, 8+len(raw))
	binary.BigEndian.PutUint32(frame[0:4], msgType)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(raw)))
	copy(frame[8:], raw)
	return frame
}

func advisoryBody(events ...map[string]any) map[string]any {
	return map[string]any{"events": events}
}

func advisoryWrite(path string, verbs ...string) map[string]any {
	return map[string]any{"path": path, "mask": verbs, "ts_unix_ms": 1_700_000_000_000}
}

// sendAdvisory runs one guest stream through the receiver. origin "" sends no
// listener origin, as an adopted VM's listener may.
func sendAdvisory(t *testing.T, r *statelessAdvisoryReceiver, origin string, frame []byte) (string, error) {
	t.Helper()
	host, guest := net.Pipe()
	defer func() { _ = host.Close() }()
	go func() {
		_, _ = guest.Write(frame)
		_ = guest.Close()
	}()
	var conn net.Conn = host
	if origin != "" {
		conn = fcvm.GuestVsockOriginConnForTest(host, fcvm.GuestVsockStreamOrigin{AppID: origin})
	}
	return r.handleGuestStream("inst-1", conn)
}

func TestStatelessAdvisoryReceiverForwardsTheListenerApp(t *testing.T) {
	rec := &advisoryForwardRecorder{}
	r := newStatelessAdvisoryReceiver(nil, rec.forward, func(string) (string, string, error) {
		return "app-from-live-record", "acct", nil
	})
	frame := advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, map[string]any{
		"app_id": "someone-elses-app", // a guest-supplied app is never trusted
		"events": []map[string]any{advisoryWrite("/app/data/app.db", "create", "modify")},
	})
	if kind, err := sendAdvisory(t, r, "app-origin", frame); err != nil {
		t.Fatalf("handleGuestStream = %q, %v", kind, err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("forwarded %d batches, want 1", len(rec.calls))
	}
	got := rec.calls[0]
	if got.instance != "inst-1" || got.appID != "app-origin" {
		t.Fatalf("forwarded instance=%q app=%q, want inst-1 / app-origin", got.instance, got.appID)
	}
	if len(got.events) != 1 || got.events[0].Path != "/app/data/app.db" || strings.Join(got.events[0].Masks, ",") != "create,modify" {
		t.Fatalf("forwarded events %+v", got.events)
	}
}

func TestStatelessAdvisoryReceiverFallsBackToTheLiveInstance(t *testing.T) {
	rec := &advisoryForwardRecorder{}
	r := newStatelessAdvisoryReceiver(nil, rec.forward, func(string) (string, string, error) {
		return "app-live", "acct", nil
	})
	frame := advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("/data/x", "create")))
	if _, err := sendAdvisory(t, r, "", frame); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0].appID != "app-live" {
		t.Fatalf("calls = %+v, want one for app-live", rec.calls)
	}

	unknown := newStatelessAdvisoryReceiver(nil, rec.forward, func(string) (string, string, error) {
		return "", "", errors.New("not live")
	})
	if kind, err := sendAdvisory(t, unknown, "", frame); kind != "identity" || err == nil {
		t.Fatalf("no identity: handleGuestStream = %q, %v; want identity error", kind, err)
	}
}

func TestStatelessAdvisoryReceiverRefusesMalformedFrames(t *testing.T) {
	long := "/data/" + strings.Repeat("x", 2000)
	tooMany := make([]map[string]any, fcvm.StatelessAdvisoryMaxEvents+1)
	for i := range tooMany {
		tooMany[i] = advisoryWrite("/data/x", "create")
	}
	cases := []struct {
		name  string
		frame []byte
	}{
		{"wrong message type", advisoryFrame(t, 1, advisoryBody(advisoryWrite("/data/x", "create")))},
		{"not JSON", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, []byte("{"))},
		{"relative path", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("data/x", "create")))},
		{"unclean path", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("/data/../etc/x", "create")))},
		{"overlong path", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite(long, "create")))},
		{"unknown verb", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("/data/x", "exec")))},
		{"no verb", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("/data/x")))},
		{"too many events", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(tooMany...))},
		{"truncated body", advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("/data/x", "create")))[:12]},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &advisoryForwardRecorder{}
			r := newStatelessAdvisoryReceiver(nil, rec.forward, nil)
			if kind, err := sendAdvisory(t, r, "app-1", tc.frame); kind != "protocol" || err == nil {
				t.Fatalf("handleGuestStream = %q, %v; want a protocol error", kind, err)
			}
			if len(rec.calls) != 0 {
				t.Fatalf("forwarded %d batches from a malformed frame", len(rec.calls))
			}
		})
	}

	oversize := make([]byte, 8)
	binary.BigEndian.PutUint32(oversize[0:4], fcvm.StatelessAdvisoryMsgType)
	binary.BigEndian.PutUint32(oversize[4:8], fcvm.StatelessAdvisoryMaxBody+1)
	r := newStatelessAdvisoryReceiver(nil, (&advisoryForwardRecorder{}).forward, nil)
	if kind, err := sendAdvisory(t, r, "app-1", oversize); kind != "protocol" || err == nil {
		t.Fatalf("oversize: handleGuestStream = %q, %v; want a protocol error", kind, err)
	}
}

func TestStatelessAdvisoryReceiverBudgetsEachInstance(t *testing.T) {
	rec := &advisoryForwardRecorder{}
	r := newStatelessAdvisoryReceiver(nil, rec.forward, nil)
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	frame := advisoryFrame(t, fcvm.StatelessAdvisoryMsgType, advisoryBody(advisoryWrite("/data/x", "create")))
	for i := 0; i < statelessAdvisoryBatchesPerMinute+3; i++ {
		if _, err := sendAdvisory(t, r, "app-1", frame); err != nil {
			t.Fatal(err)
		}
	}
	if len(rec.calls) != statelessAdvisoryBatchesPerMinute {
		t.Fatalf("forwarded %d batches in one minute, want the budget of %d", len(rec.calls), statelessAdvisoryBatchesPerMinute)
	}
	now = now.Add(time.Minute)
	if _, err := sendAdvisory(t, r, "app-1", frame); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != statelessAdvisoryBatchesPerMinute+1 {
		t.Fatalf("the budget did not refill after a minute: %d batches", len(rec.calls))
	}
}

type advisoryAuditRecorder struct {
	kind    string
	subject *string
	data    map[string]any
	calls   int
}

func (r *advisoryAuditRecorder) Emit(_ context.Context, kind string, accountID *string, data map[string]any) {
	r.calls++
	r.kind, r.subject, r.data = kind, accountID, data
}

type advisoryApps struct {
	app state.App
	err error
}

func (a advisoryApps) AppByID(context.Context, string) (state.App, error) { return a.app, a.err }

func TestStatelessAdvisoryAuditorWritesTheReceiverRowShape(t *testing.T) {
	rec := &advisoryAuditRecorder{}
	a := &statelessAdvisoryAuditor{audit: rec, apps: advisoryApps{app: state.App{ID: "app-1", AccountID: "acct-1"}}}
	events := []fcvm.AdvisoryEvent{
		{Path: "/app/data/app.db", Masks: []string{"create", "modify"}, TsUnix: 1},
		{Path: "/data/cache", Masks: []string{"create"}, TsUnix: 2},
	}
	if err := a.Forward(context.Background(), "inst-1", "app-1", events); err != nil {
		t.Fatal(err)
	}
	if rec.kind != "stateless.advisory" || rec.subject == nil || *rec.subject != "acct-1" {
		t.Fatalf("emitted kind=%q subject=%v", rec.kind, rec.subject)
	}
	if rec.data["instance"] != "inst-1" || rec.data["app_id"] != "app-1" || rec.data["count"] != 2 || rec.data["severity"] != "high" {
		t.Fatalf("row data = %+v", rec.data)
	}
	rows, _ := rec.data["events"].([]map[string]any)
	if len(rows) != 2 || rows[0]["mask"] != "create,modify" || rows[0]["path"] != "/app/data/app.db" {
		t.Fatalf("row events = %+v", rec.data["events"])
	}
}

func TestStatelessAdvisoryAuditorSendsTheLiveSummary(t *testing.T) {
	var payloads []string
	a := &statelessAdvisoryAuditor{
		audit:  &advisoryAuditRecorder{},
		apps:   advisoryApps{app: state.App{ID: "app-1", AccountID: "acct-1"}},
		notify: func(_ context.Context, payload string) error { payloads = append(payloads, payload); return nil },
	}
	_ = a.Forward(context.Background(), "inst-1", "app-1", []fcvm.AdvisoryEvent{{Path: "/data/x", Masks: []string{"create"}}})
	if len(payloads) != 1 {
		t.Fatalf("sent %d summaries, want 1", len(payloads))
	}
	var summary map[string]any
	if err := json.Unmarshal([]byte(payloads[0]), &summary); err != nil {
		t.Fatal(err)
	}
	if summary["app_id"] != "app-1" || summary["instance"] != "inst-1" || summary["n"] != float64(1) || summary["sample_path"] != "/data/x" {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestStatelessAdvisoryAuditorAppLookup(t *testing.T) {
	events := []fcvm.AdvisoryEvent{{Path: "/app/data/x", Masks: []string{"create"}}}

	gone := &advisoryAuditRecorder{}
	a := &statelessAdvisoryAuditor{audit: gone, apps: advisoryApps{err: state.ErrNotFound}}
	_ = a.Forward(context.Background(), "inst-1", "app-1", events)
	if gone.calls != 0 {
		t.Fatalf("wrote a row for a deleted app")
	}

	blip := &advisoryAuditRecorder{}
	a = &statelessAdvisoryAuditor{audit: blip, apps: advisoryApps{err: errors.New("connection reset")}}
	_ = a.Forward(context.Background(), "inst-1", "app-1", events)
	if blip.calls != 1 || blip.subject != nil || blip.data["severity"] != "warn" {
		t.Fatalf("lookup failure: calls=%d subject=%v data=%+v; want one unattributed warn row", blip.calls, blip.subject, blip.data)
	}
}
