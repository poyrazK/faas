// adr: 20261022183526
package state_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioRealtimeGroupResolvesWithinTheRunAccount(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			base, account, app := *f.Base(), f.Account(), f.App()
			groups := base.(state.ScenarioRealtimeGroupStore)
			if backend == "memory" {
				// Consumer groups need durable PostgreSQL storage.
				if _, err := groups.ScenarioRealtimeConsumerGroup(ctx, account.ID, state.ActionCaptureTestRun, app.Slug, uuid.NewString(), "orders", "billing"); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("memory group lookup: %v", err)
				}
				return
			}
			endpoint, err := base.(state.ManagedRealtimeEndpointStore).CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{AccountID: account.ID, AppID: app.ID,
				CallbackURL: "https://example.com", ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
				CallbackAuthTokenSealed: []byte("callback"), AuthTokenSealed: []byte("client"), Enabled: true}, 5, 10)
			if err != nil {
				t.Fatal(err)
			}
			created, err := base.(state.RealtimeConsumerGroupStore).CreateRealtimeConsumerGroup(ctx, endpoint.ID, "orders", "billing",
				state.RealtimeConsumerGroupConfig{AfterSequence: "0", LeaseSeconds: 45, MaxAttempts: 4})
			if err != nil {
				t.Fatal(err)
			}
			for _, address := range []string{app.Slug, "primary"} {
				got, err := groups.ScenarioRealtimeConsumerGroup(ctx, account.ID, state.ActionCaptureTestRun, address, endpoint.ID, "orders", "billing")
				if err != nil || got.GroupID != created.GroupID || got.EndpointID != endpoint.ID || got.LeaseSeconds != 45 || got.MaxAttempts != 4 || !got.EndpointEnabled ||
					got.Filter.EventTypes == nil || got.CreatedAt.IsZero() {
					t.Fatalf("group by %q = %+v %v", address, got, err)
				}
			}
			if got, err := groups.ScenarioRealtimeWorkerGroup(ctx, account.ID, created.GroupID); err != nil || got.Name != "billing" || got.Channel != "orders" {
				t.Fatalf("worker group = %+v %v", got, err)
			}
			for _, miss := range []struct{ slug, endpoint, channel, name string }{
				{"other-app", endpoint.ID, "orders", "billing"},
				{app.Slug, uuid.NewString(), "orders", "billing"},
				{app.Slug, endpoint.ID, "payments", "billing"},
				{app.Slug, endpoint.ID, "orders", "shipping"},
			} {
				if _, err := groups.ScenarioRealtimeConsumerGroup(ctx, account.ID, state.ActionCaptureTestRun, miss.slug, miss.endpoint, miss.channel, miss.name); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("group %+v: %v", miss, err)
				}
			}
			for _, miss := range []struct{ account, group string }{{uuid.NewString(), created.GroupID}, {account.ID, uuid.NewString()}, {account.ID, "not-a-uuid"}} {
				if _, err := groups.ScenarioRealtimeWorkerGroup(ctx, miss.account, miss.group); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("worker group %+v: %v", miss, err)
				}
			}
		})
	}
}
