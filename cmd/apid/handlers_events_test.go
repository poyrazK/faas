// Negative + integration tests for the SSE live-update handler
// (M7.5 slice 6).
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// recordingNotifier is a fake Notifier that lets the test inject
// notifications into the SSE stream without a real Postgres.
type recordingNotifier struct {
	out chan db.Notification
}

func newRecordingNotifier() *recordingNotifier {
	return &recordingNotifier{out: make(chan db.Notification, 16)}
}

func (n *recordingNotifier) Notify(_ context.Context, _, payload string) error {
	return nil
}
func (n *recordingNotifier) Subscribe(_ context.Context, chs []string) (<-chan db.Notification, func(), error) {
	return n.out, func() {}, nil
}
func (n *recordingNotifier) publish(channel, payload string) {
	n.out <- db.Notification{Channel: channel, Payload: payload}
}

// TestEvents_FiltersByAccount confirms a notification carrying an
// account_id is delivered only to that account's SSE stream; a
// foreign account_id is dropped.
func TestEvents_FiltersByAccount(t *testing.T) {
	e := setup(t, api.PlanPro)
	notif := newRecordingNotifier()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(e.store, log, "example.com", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0).handler()

	res := make(chan string, 1)
	go func() {
		hReq := httptest.NewRequest("GET", "/v1/events", nil)
		hReq.Header.Set("Authorization", "Bearer "+e.key)
		hRec := httptest.NewRecorder()
		srv.ServeHTTP(hRec, hReq)
		res <- hRec.Body.String()
	}()
	// Give the handler a tick to subscribe.
	time.Sleep(50 * time.Millisecond)

	// Owned app — should pass through.
	notif.publish(db.NotifyAppChanged, `{"app_id":"my-app","account_id":"`+e.acct.ID+`"}`)
	// Foreign account — must be dropped.
	notif.publish(db.NotifyAppChanged, `{"app_id":"strangers","account_id":"`+"ffffffff-ffff-ffff-ffff-ffffffffffff"+`"}`)
	// Unparseable payload — passes through (safe default).
	notif.publish(db.NotifyAppChanged, "not-json")
	time.Sleep(50 * time.Millisecond)

	// Close the recorder via request context cancel: httptest
	// doesn't expose a clean way; use a short timeout + force the
	// goroutine to finish by closing the notifier's channel and
	// timing out the test.
	close(notif.out) // EOF → handler returns
	body := <-res

	if !strings.Contains(body, `event: app_changed`) {
		t.Errorf("body missing event header\n%s", body)
	}
	if !strings.Contains(body, `"account_id":"`+e.acct.ID+`"`) {
		t.Errorf("body missing own-account frame\n%s", body)
	}
	if strings.Contains(body, "strangers") {
		t.Errorf("body leaked a foreign-account frame\n%s", body)
	}
}

// TestEvents_RequiresAuth confirms a request without cookie OR
// bearer token returns 401 RFC 7807 immediately, never an SSE
// handshake.
func TestEvents_RequiresAuth(t *testing.T) {
	e := setup(t, api.PlanPro)
	notif := newRecordingNotifier()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(e.store, log, "example.com", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0).handler()

	req := httptest.NewRequest("GET", "/v1/events", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != 401 {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Errorf("ct = %q, want problem+json", ct)
	}
	_ = state.App{}
}
