package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStore_RealtimePatchEmptyCollections(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-empty-patch")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, populated := range []bool{false, true} {
		if populated {
			audience, algorithms, origins := []string{"customer"}, []string{"RS256"}, []string{"https://example.com"}
			if _, err := s.UpdateManagedRealtimeEndpoint(ctx, endpoint.ID, state.UpdateManagedRealtimeEndpointParams{
				AuthAudience: &audience, AuthAlgorithms: &algorithms, AllowedOrigins: &origins,
			}); err != nil {
				t.Fatal(err)
			}
		}
		empty := []string{}
		updated, err := s.UpdateManagedRealtimeEndpoint(ctx, endpoint.ID, state.UpdateManagedRealtimeEndpointParams{
			AuthAudience: &empty, AuthAlgorithms: &empty, AllowedOrigins: &empty,
		})
		if err != nil {
			t.Fatalf("clear collections (previously populated=%v): %v", populated, err)
		}
		if len(updated.AuthAudience) != 0 || len(updated.AuthAlgorithms) != 0 || len(updated.AllowedOrigins) != 0 {
			t.Fatalf("empty patch retained values: %+v", updated)
		}
		callback := "https://example.com/realtime/updated"
		if _, err := s.UpdateManagedRealtimeEndpoint(ctx, endpoint.ID, state.UpdateManagedRealtimeEndpointParams{CallbackURL: &callback}); err != nil {
			t.Fatalf("unrelated callback update after clear: %v", err)
		}
	}
}

// Finalizing a token rotation clears the previous token to an empty value.
// The sealed columns are NOT NULL, so an empty patch must be stored as ”.
func TestPgStore_RealtimePatchEmptySealedTokens(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-realtime-empty-sealed")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	previous := []byte("sealed-previous")
	if _, err := s.UpdateManagedRealtimeEndpoint(ctx, endpoint.ID, state.UpdateManagedRealtimeEndpointParams{
		AuthTokenPreviousSealed: &previous,
	}); err != nil {
		t.Fatal(err)
	}
	empty := []byte{}
	updated, err := s.UpdateManagedRealtimeEndpoint(ctx, endpoint.ID, state.UpdateManagedRealtimeEndpointParams{
		AuthTokenPreviousSealed: &empty, AuthTokenSealed: &empty, ClearAuthTokenPreviousExpiresAt: true,
	})
	if err != nil {
		t.Fatalf("clear sealed tokens: %v", err)
	}
	if len(updated.AuthTokenPreviousSealed) != 0 || len(updated.AuthTokenSealed) != 0 {
		t.Fatalf("empty patch retained tokens: %+v", updated)
	}
}
