package state

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func liveCapsEndpoint(t *testing.T, m *MemStore, plan api.Plan) string {
	t.Helper()
	ctx := context.Background()
	account, err := m.CreateAccount(ctx, "live-caps-"+uuid.NewString()+"@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(ctx, App{AccountID: account.ID, Slug: "live-caps-" + uuid.NewString()[:8], Status: AppActive, RAMMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := m.CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID,
		CallbackURL: "https://example.com/callback", Enabled: true}, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	return endpoint.ID
}

// adr: 1323 (amendment of 2026-10-31) — per-user state caps count live rows
// only, so none is used up for good.
func TestMemStoreRealtimeLiveCaps(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		run  func(t *testing.T, m *MemStore, ep string)
	}{
		{"an expired inbox frees its slot", func(t *testing.T, m *MemStore, ep string) {
			for i := range ManagedRealtimeInboxMaxPrincipals {
				if _, err := m.AppendManagedRealtimeInboxMessage(ctx, ep, fmt.Sprintf("user-%d", i), []byte("x"), false, fmt.Sprintf("m-%d", i)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.AppendManagedRealtimeInboxMessage(ctx, ep, "next", []byte("x"), false, "m-next"); !errors.Is(err, ErrManagedRealtimeHistoryLimit) {
				t.Fatalf("inbox past the cap = %v, want the history limit", err)
			}
			key, err := managedRealtimeInboxKey("user-0")
			if err != nil {
				t.Fatal(err)
			}
			for i := range m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: ep, channel: key}].messages {
				m.managedRealtimeInboxStreams[managedRealtimeHistoryKey{endpointID: ep, channel: key}].messages[i].CreatedAt = time.Now().Add(-ManagedRealtimeInboxRetention - time.Minute)
			}
			if _, err := m.AppendManagedRealtimeInboxMessage(ctx, ep, "next", []byte("x"), false, "m-next"); err != nil {
				t.Fatalf("inbox after an expired one = %v, want admitted", err)
			}
		}},
		{"a read marker expires after 30 days", func(t *testing.T, m *MemStore, ep string) {
			if _, err := m.AppendManagedRealtimeChannelMessage(ctx, ep, "orders", []byte("x"), false, ""); err != nil {
				t.Fatal(err)
			}
			for i := range ManagedRealtimeMaxReadMarkers {
				if _, err := m.AdvanceManagedRealtimeReadProgress(ctx, ep, fmt.Sprintf("user-%d", i), "orders", false, 1); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.AdvanceManagedRealtimeReadProgress(ctx, ep, "next", "orders", false, 1); !errors.Is(err, ErrManagedRealtimeDurableCursorLimit) {
				t.Fatalf("marker past the cap = %v, want the limit", err)
			}
			for key, marker := range m.managedRealtimeReadProgress {
				marker.updated = time.Now().Add(-ManagedRealtimeReadMarkerRetention - time.Minute)
				m.managedRealtimeReadProgress[key] = marker
			}
			if _, err := m.AdvanceManagedRealtimeReadProgress(ctx, ep, "next", "orders", false, 1); err != nil {
				t.Fatalf("marker beside expired ones = %v, want admitted", err)
			}
			if removed, err := m.PruneExpiredManagedRealtimeReadMarkers(ctx, 1000); err != nil || removed != 1000 {
				t.Fatalf("prune = (%d, %v), want a full batch of 1000", removed, err)
			}
		}},
		{"preferences are capped per account and freed by a reset", func(t *testing.T, m *MemStore, ep string) {
			limits, _ := api.RealtimeLimitsFor(api.PlanHobby)
			quiet := api.RealtimeNotificationPreferences{Enabled: false, Categories: map[string]bool{}}
			for i := range limits.NotificationPreferencesPerAccount {
				if err := m.PutManagedRealtimeNotificationPreferences(ctx, ep, fmt.Sprintf("user-%d", i), quiet); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.PutManagedRealtimeNotificationPreferences(ctx, ep, "next", quiet); !errors.Is(err, ErrManagedRealtimeDurableCursorLimit) {
				t.Fatalf("user past the account cap = %v, want the limit", err)
			}
			if err := m.PutManagedRealtimeNotificationPreferences(ctx, ep, "user-0", api.RealtimeNotificationPreferences{Enabled: true}); err != nil {
				t.Fatal(err)
			}
			if err := m.PutManagedRealtimeNotificationPreferences(ctx, ep, "next", quiet); err != nil {
				t.Fatalf("user after another's reset = %v, want admitted", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMemStore()
			tt.run(t, m, liveCapsEndpoint(t, m, api.PlanHobby))
		})
	}
}
