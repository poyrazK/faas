package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/state"
)

type recoveryReviewGuardStore struct {
	*state.MemStore
	allowed  bool
	reason   string
	checkErr error
	checks   int
}

func (s *recoveryReviewGuardStore) WorkflowRecoveryReviewNotificationAllowed(context.Context, state.AppWebhookDelivery, time.Time) (bool, string, error) {
	s.checks++
	return s.allowed, s.reason, s.checkErr
}
func TestRecoveryReviewNotificationDispatcher(t *testing.T) {
	for _, scenario := range []string{"dismissed", "approved", "stale", "applied", "unknown", "retry"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s := &recoveryReviewGuardStore{MemStore: state.NewMemStore(), allowed: scenario == "retry", reason: scenario}
			if scenario == "unknown" {
				s.checkErr = errors.New("database temporarily unavailable")
			}
			appID, accountID := "review-dispatch-app", "review-dispatch-account"
			if _, err := s.CreateApp(ctx, state.App{ID: appID, AccountID: accountID, Slug: "review-dispatch", Status: "ready"}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(503)
				} else {
					w.WriteHeader(204)
				}
			}))
			defer srv.Close()
			loader, sealed := identityForSealedBlob(t)
			hook := newTestAppWebhook(t, s.MemStore, appID, accountID, srv.URL, state.AppWebhookRetryDefault)
			if _, err := s.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
				t.Fatal(err)
			}
			delivery, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{WebhookID: hook.ID, AppID: appID, AccountID: accountID, Event: state.AppWebhookEventRecoveryReviewReady, Payload: json.RawMessage(`{"event_id":"stable-review-event"}`)})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(time.Second)
			rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
			if err != nil || len(rows) != 1 {
				t.Fatalf("claim=%+v %v", rows, err)
			}
			d := NewDispatcher(s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			unseals := 0
			d.IdentityLoader = func() []*age.X25519Identity { unseals++; return loader() }
			d.HTTPClient = srv.Client()
			d.Now = func() time.Time { return now }
			d.deliverOne(ctx, rows[0])
			retained, err := s.AppWebhookDeliveryByID(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "unknown" {
				if retained.Status != state.AppWebhookDeliveryInFlight || calls != 0 || unseals != 0 {
					t.Fatalf("unknown did not retain lease=%+v calls=%d", retained, calls)
				}
			} else if scenario != "retry" {
				if retained.Status != state.AppWebhookDeliveryDead || calls != 0 || unseals != 0 || !strings.Contains(retained.LastError, "suppressed: "+scenario) {
					t.Fatalf("suppression=%+v calls=%d", retained, calls)
				}
			} else {
				if retained.Status != state.AppWebhookDeliveryPending || calls != 1 {
					t.Fatalf("first retry=%+v calls=%d", retained, calls)
				}
				now = retained.NextAttemptAt.Add(time.Second)
				rows, err = s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
				if err != nil || len(rows) != 1 || rows[0].ID != delivery.ID || string(rows[0].Payload) != string(delivery.Payload) {
					t.Fatalf("retry claim=%+v %v", rows, err)
				}
				d.deliverOne(ctx, rows[0])
				retained, err = s.AppWebhookDeliveryByID(ctx, delivery.ID)
				if err != nil || retained.Status != state.AppWebhookDeliverySucceeded || calls != 2 || s.checks != 2 {
					t.Fatalf("retry success=%+v checks=%d calls=%d %v", retained, s.checks, calls, err)
				}
			}
		})
	}
}

func (s *recoveryReviewGuardStore) WorkflowRecoveryReviewCompletionNotificationAllowed(context.Context, state.AppWebhookDelivery) (bool, string, error) {
	s.checks++
	return s.allowed, s.reason, s.checkErr
}
func TestRecoveryReviewCompletionDispatcher(t *testing.T) {
	for _, scenario := range []string{"payload_mismatch", "capture_mismatch", "review_not_retained", "unknown", "retry"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			s := &recoveryReviewGuardStore{MemStore: state.NewMemStore(), allowed: scenario == "retry", reason: scenario}
			if scenario == "unknown" {
				s.checkErr = errors.New("database temporarily unavailable")
			}
			appID, accountID := "review-dispatch-app", "review-dispatch-account"
			if _, err := s.CreateApp(ctx, state.App{ID: appID, AccountID: accountID, Slug: "review-dispatch", Status: "ready"}); err != nil {
				t.Fatal(err)
			}
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(503)
				} else {
					w.WriteHeader(204)
				}
			}))
			defer srv.Close()
			loader, sealed := identityForSealedBlob(t)
			hook := newTestAppWebhook(t, s.MemStore, appID, accountID, srv.URL, state.AppWebhookRetryDefault)
			if _, err := s.UpdateAppWebhook(ctx, hook.ID, state.UpdateAppWebhookParams{WebhookSecretSealed: &sealed}); err != nil {
				t.Fatal(err)
			}
			delivery, err := s.RecordAppWebhookDelivery(ctx, state.AppWebhookDelivery{WebhookID: hook.ID, AppID: appID, AccountID: accountID, Event: state.AppWebhookEventRecoveryReviewFinished, Payload: json.RawMessage(`{"event_id":"stable-review-event"}`)})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Add(time.Second)
			rows, err := s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
			if err != nil || len(rows) != 1 {
				t.Fatalf("claim=%+v %v", rows, err)
			}
			d := NewDispatcher(s, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
			unseals := 0
			d.IdentityLoader = func() []*age.X25519Identity { unseals++; return loader() }
			d.HTTPClient = srv.Client()
			d.Now = func() time.Time { return now }
			d.deliverOne(ctx, rows[0])
			retained, err := s.AppWebhookDeliveryByID(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "unknown" {
				if retained.Status != state.AppWebhookDeliveryInFlight || calls != 0 || unseals != 0 {
					t.Fatalf("unknown did not retain lease=%+v calls=%d", retained, calls)
				}
			} else if scenario != "retry" {
				if retained.Status != state.AppWebhookDeliveryDead || calls != 0 || unseals != 0 || !strings.Contains(retained.LastError, "suppressed: "+scenario) {
					t.Fatalf("suppression=%+v calls=%d", retained, calls)
				}
			} else {
				if retained.Status != state.AppWebhookDeliveryPending || calls != 1 {
					t.Fatalf("first retry=%+v calls=%d", retained, calls)
				}
				now = retained.NextAttemptAt.Add(time.Second)
				rows, err = s.ClaimDueAppWebhookDeliveries(ctx, 1, now)
				if err != nil || len(rows) != 1 || rows[0].ID != delivery.ID || string(rows[0].Payload) != string(delivery.Payload) {
					t.Fatalf("retry claim=%+v %v", rows, err)
				}
				d.deliverOne(ctx, rows[0])
				retained, err = s.AppWebhookDeliveryByID(ctx, delivery.ID)
				if err != nil || retained.Status != state.AppWebhookDeliverySucceeded || calls != 2 || s.checks != 2 {
					t.Fatalf("retry success=%+v checks=%d calls=%d %v", retained, s.checks, calls, err)
				}
			}
		})
	}
}
