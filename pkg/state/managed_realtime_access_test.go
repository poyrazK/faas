package state_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRealtimeAccessStoresPersistConcurrentDeniesAndSessionCutoff(t *testing.T) {
	for _, name := range []string{"memory", "postgres"} {
		t.Run(name, func(t *testing.T) {
			var store contractTestStore
			if name == "memory" {
				store = state.NewMemStore()
			} else {
				pg, _ := pgStore(t)
				store = pg
			}
			access := store.(state.ManagedRealtimeAccessStore)
			ctx := context.Background()
			owner, err := store.CreateAccount(ctx, "realtime-access-"+uuid.NewString()+"@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: owner.ID, Slug: "access-" + uuid.NewString()[:8]})
			if err != nil {
				t.Fatal(err)
			}
			endpoint := pgManagedRealtimeEndpoint(owner.ID, app.ID)
			endpoint.AuthMode = "oidc_jwt"
			endpoint.AuthIssuer = "https://issuer.example"
			endpoint.AuthJWKSURL = "https://issuer.example/jwks"
			endpoint.AuthAlgorithms = []string{"RS256"}
			ep, err := store.CreateManagedRealtimeEndpointIfUnderQuota(ctx, endpoint, 10, 50)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			failures := make(chan error, 8)
			for i := range 8 {
				wg.Go(func() {
					_, err := access.ChangeManagedRealtimeAccess(ctx, ep.ID, "user", "revoke_channel", string(rune('a'+i)))
					failures <- err
				})
			}
			wg.Wait()
			close(failures)
			for err := range failures {
				if err != nil {
					t.Fatal(err)
				}
			}
			row, err := access.ReadManagedRealtimeAccess(ctx, ep.ID, "user")
			if err != nil || len(row.DeniedChannels) != 8 || row.Revision != 8 {
				t.Fatalf("concurrent controls: %+v %v", row, err)
			}
			row, err = access.ChangeManagedRealtimeAccess(ctx, ep.ID, "user", "revoke_sessions", "")
			if err != nil || row.SessionsRevokedBefore == nil {
				t.Fatalf("cutoff: %+v %v", row, err)
			}
			cutoff := *row.SessionsRevokedBefore
			row, err = access.ChangeManagedRealtimeAccess(ctx, ep.ID, "user", "restore_channel", "a")
			if err != nil || len(row.DeniedChannels) != 7 || row.SessionsRevokedBefore == nil || !row.SessionsRevokedBefore.Equal(cutoff) {
				t.Fatalf("restore preserved cutoff: %+v %v", row, err)
			}
			row.DeniedChannels[0] = "mutated"
			reloaded, err := access.ReadManagedRealtimeAccess(ctx, ep.ID, "user")
			if err != nil || reloaded.DeniedChannels[0] == "mutated" {
				t.Fatalf("snapshot alias: %+v %v", reloaded, err)
			}
			if err = store.DeleteManagedRealtimeEndpoint(ctx, ep.ID); err != nil {
				t.Fatal(err)
			}
			row, err = access.ReadManagedRealtimeAccess(ctx, ep.ID, "user")
			if err != nil || row.Revision != 0 {
				t.Fatalf("endpoint cleanup: %+v %v", row, err)
			}
		})
	}
}
