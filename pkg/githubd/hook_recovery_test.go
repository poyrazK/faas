package githubd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHookRecoveryDurableBoundAndAcceptedInbox(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := NewPGWebhookDeliveryStore(pool)
	ctx := t.Context()
	now := time.Now().UTC()
	for attempt := 0; attempt < 4; attempt++ {
		claimed, err := store.ClaimTransportRecovery(ctx, "missed", now.Add(time.Duration(attempt)*20*time.Minute))
		if err != nil || claimed != (attempt < 3) {
			t.Fatalf("attempt %d claimed=%v err=%v", attempt, claimed, err)
		}
	}
	if _, err := store.Enqueue(ctx, WebhookDelivery{DeliveryID: "accepted", EventType: "push", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimTransportRecovery(ctx, "accepted", now)
	if err != nil || claimed {
		t.Fatalf("accepted event redelivered %v %v", claimed, err)
	}
	if err := store.DelayTransportRecovery(ctx, "limited", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	restarted := NewPGWebhookDeliveryStore(pool)
	claimed, err = restarted.ClaimTransportRecovery(ctx, "other", now.Add(time.Minute))
	if err != nil || claimed {
		t.Fatalf("provider rate limit lost across restart: %v %v", claimed, err)
	}
}

type recoveryClaims struct {
	ids   []string
	delay time.Time
}

func (s *recoveryClaims) ClaimTransportRecovery(_ context.Context, id string, _ time.Time) (bool, error) {
	s.ids = append(s.ids, id)
	return true, nil
}
func (s *recoveryClaims) DelayTransportRecovery(_ context.Context, _ string, at time.Time) error {
	s.delay = at
	return nil
}
func (s *recoveryClaims) PruneTransportRecoveries(context.Context) error { return nil }
func TestHookMonitorRecoversOnlyMissedTransientDeliveries(t *testing.T) {
	now := time.Now().UTC()
	at := now.Add(-3 * time.Minute)
	claims := &recoveryClaims{}
	posts := 0
	deliveries := []HookDelivery{{ID: 1, GUID: "missed", Event: "push", StatusCode: 503, DeliveredAt: at}, {ID: 2, GUID: "auth", Event: "push", StatusCode: 401, DeliveredAt: at}, {ID: 3, GUID: "accepted", Event: "push", StatusCode: 500, DeliveredAt: at}, {ID: 4, GUID: "accepted", Event: "push", StatusCode: 200, DeliveredAt: at}, {ID: 5, GUID: "too-new", Event: "push", StatusCode: 500, DeliveredAt: now}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(deliveries)
			return
		}
		if r.URL.Path != "/app/hook/deliveries/1/attempts" {
			t.Errorf("unexpected retry %s", r.URL.Path)
		}
		posts++
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	monitor := &HookDeliveryMonitor{Client: srv.Client(), BaseURL: srv.URL, MintJWT: func() (string, error) { return "jwt", nil }, Recovery: claims, Now: func() time.Time { return now }}
	if _, err := monitor.PollOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || len(claims.ids) != 1 || claims.ids[0] != "missed" {
		t.Fatalf("unsafe retries: %v %d", claims.ids, posts)
	}
}

func TestHookRecoveryRateLimitDefersAndStopsBatch(t *testing.T) {
	now := time.Now().UTC()
	claims := &recoveryClaims{}
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode([]HookDelivery{{ID: 1, GUID: "first", Event: "push", StatusCode: 500, DeliveredAt: now.Add(-3 * time.Minute)}, {ID: 2, GUID: "second", Event: "push", StatusCode: 500, DeliveredAt: now.Add(-3 * time.Minute)}})
			return
		}
		posts++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	monitor := &HookDeliveryMonitor{Client: srv.Client(), BaseURL: srv.URL, MintJWT: func() (string, error) { return "jwt", nil }, Recovery: claims, Now: func() time.Time { return now }}
	if _, err := monitor.PollOnce(t.Context()); err == nil {
		t.Fatal("rate limit was hidden")
	}
	if posts != 1 || !claims.delay.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("rate limit ignored: posts=%d retry=%v", posts, claims.delay)
	}
}

func TestHookMonitorFollowsOnlyProviderPagination(t *testing.T) {
	now := time.Now().UTC()
	var base string
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		if r.URL.Query().Get("cursor") == "next" {
			json.NewEncoder(w).Encode([]HookDelivery{{ID: 1, GUID: "older", StatusCode: 200, DeliveredAt: now.Add(-3 * time.Minute)}})
			return
		}
		w.Header().Set("Link", "<"+base+"/app/hook/deliveries?per_page=100&cursor=next>; rel=\"next\"")
		json.NewEncoder(w).Encode([]HookDelivery{{ID: 2, GUID: "newer", StatusCode: 200, DeliveredAt: now.Add(-2 * time.Minute)}})
	}))
	defer srv.Close()
	base = srv.URL
	monitor := &HookDeliveryMonitor{Client: srv.Client(), BaseURL: base, MintJWT: func() (string, error) { return "jwt", nil }, Now: func() time.Time { return now }}
	summary, err := monitor.PollOnce(t.Context())
	if err != nil || pages != 2 || summary.RecentDelivered != 2 {
		t.Fatalf("pagination incomplete: %+v pages=%d err=%v", summary, pages, err)
	}
}
