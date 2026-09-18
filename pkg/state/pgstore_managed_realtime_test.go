package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func pgManagedRealtimeEndpoint(accountID, appID string) state.ManagedRealtimeEndpoint {
	return state.ManagedRealtimeEndpoint{
		AccountID:               accountID,
		AppID:                   appID,
		CallbackURL:             "https://example.com/realtime/" + uuid.NewString(),
		ConnectPath:             "/hooks/connect",
		MessagePath:             "/hooks/message",
		DisconnectPath:          "/hooks/disconnect",
		CallbackAuthTokenSealed: []byte("sealed-callback-token"),
		AuthTokenSealed:         []byte("sealed-auth-token"),
		Enabled:                 true,
	}
}

func TestPgStore_ManagedRealtimeDrainOperationClaim(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-managed-realtime-drain-claim")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	op, err := s.CreateManagedRealtimeDrainOperation(ctx, state.ManagedRealtimeDrainOperationInput{
		AccountID: accountID, AppID: appID, EndpointID: endpoint.ID,
		Reason: "deploy", Matched: 1, ConnectionIDs: []string{"connection-1"}, Limit: 10,
	})
	if err != nil {
		t.Fatalf("CreateManagedRealtimeDrainOperation: %v", err)
	}

	claims, err := s.ClaimManagedRealtimeDrainOperations(ctx, 1, time.Minute)
	if err != nil {
		t.Fatalf("ClaimManagedRealtimeDrainOperations: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %d, want 1", len(claims))
	}
	if claims[0].Operation.ID != op.ID || claims[0].Operation.Attempts != 1 || claims[0].ClaimToken == "" {
		t.Fatalf("claim = %+v, want operation %s with attempt 1 and a token", claims[0], op.ID)
	}
}

func TestPgStore_ManagedRealtimeEndpointRoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-managed-realtime")

	created, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created endpoint has empty id")
	}

	got, err := s.ManagedRealtimeEndpointByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("ManagedRealtimeEndpointByID: %v", err)
	}
	if got.AppID != appID || got.AccountID != accountID || got.CallbackURL != created.CallbackURL {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	byApp, err := s.ListManagedRealtimeEndpointsForApp(ctx, appID)
	if err != nil {
		t.Fatalf("ListManagedRealtimeEndpointsForApp: %v", err)
	}
	if len(byApp) != 1 || byApp[0].ID != created.ID {
		t.Fatalf("app list = %+v, want one endpoint %s", byApp, created.ID)
	}
	byAccount, err := s.ListManagedRealtimeEndpointsForAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("ListManagedRealtimeEndpointsForAccount: %v", err)
	}
	if len(byAccount) != 1 || byAccount[0].ID != created.ID {
		t.Fatalf("account list = %+v, want one endpoint %s", byAccount, created.ID)
	}
	all, err := s.ListManagedRealtimeEndpoints(ctx)
	if err != nil {
		t.Fatalf("ListManagedRealtimeEndpoints: %v", err)
	}
	if len(all) != 1 || all[0].ID != created.ID {
		t.Fatalf("all list = %+v, want one endpoint %s", all, created.ID)
	}

	enabled := false
	newURL := "https://example.com/realtime/updated"
	newConnectPath := "/hooks/connect-v2"
	newPath := "/hooks/message-v2"
	newDisconnectPath := "/hooks/disconnect-v2"
	newCallbackToken := []byte("sealed-callback-token-v2")
	newAuthToken := []byte("sealed-auth-token-v2")
	updated, err := s.UpdateManagedRealtimeEndpoint(ctx, created.ID, state.UpdateManagedRealtimeEndpointParams{
		CallbackURL:             &newURL,
		ConnectPath:             &newConnectPath,
		MessagePath:             &newPath,
		DisconnectPath:          &newDisconnectPath,
		CallbackAuthTokenSealed: &newCallbackToken,
		AuthTokenSealed:         &newAuthToken,
		Enabled:                 &enabled,
	})
	if err != nil {
		t.Fatalf("UpdateManagedRealtimeEndpoint: %v", err)
	}
	if updated.Enabled || updated.CallbackURL != newURL || updated.ConnectPath != newConnectPath ||
		updated.MessagePath != newPath || updated.DisconnectPath != newDisconnectPath ||
		string(updated.CallbackAuthTokenSealed) != string(newCallbackToken) || string(updated.AuthTokenSealed) != string(newAuthToken) {
		t.Fatalf("update did not apply: %+v", updated)
	}
	all, err = s.ListManagedRealtimeEndpoints(ctx)
	if err != nil {
		t.Fatalf("ListManagedRealtimeEndpoints after disable: %v", err)
	}
	if len(all) != 1 || all[0].ID != created.ID || all[0].Enabled {
		t.Fatalf("all list after disable = %+v, want disabled endpoint %s", all, created.ID)
	}

	duplicate := pgManagedRealtimeEndpoint(accountID, appID)
	duplicate.ID = created.ID
	if _, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, duplicate, 10, 50); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate endpoint id error = %v, want ErrConflict", err)
	}

	if err := s.DeleteManagedRealtimeEndpoint(ctx, created.ID); err != nil {
		t.Fatalf("DeleteManagedRealtimeEndpoint: %v", err)
	}
	if _, err := s.ManagedRealtimeEndpointByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("post-delete lookup = %v, want ErrNotFound", err)
	}
	if err := s.DeleteManagedRealtimeEndpoint(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestPgStore_ManagedRealtimeEndpointQuotasAndOwnership(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-managed-realtime-quota")

	if _, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 1, 50); err != nil {
		t.Fatalf("first endpoint: %v", err)
	}
	_, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 1, 50)
	var quotaErr *state.ManagedRealtimeEndpointQuotaError
	if !errors.As(err, &quotaErr) || quotaErr.Scope != state.ManagedRealtimeEndpointQuotaScopeApp {
		t.Fatalf("per-app overflow = %v, want app quota error", err)
	}

	otherApp, err := s.CreateApp(ctx, state.App{
		AccountID: accountID,
		Slug:      "pg-managed-realtime-" + uuid.NewString(),
		RAMMB:     256,
	})
	if err != nil {
		t.Fatalf("CreateApp for account quota: %v", err)
	}
	_, err = s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, otherApp.ID), 10, 1)
	quotaErr = nil
	if !errors.As(err, &quotaErr) || quotaErr.Scope != state.ManagedRealtimeEndpointQuotaScopeAccount {
		t.Fatalf("per-account overflow = %v, want account quota error", err)
	}

	_, err = s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, "00000000-0000-0000-0000-000000000000"), 10, 50)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("missing app = %v, want ErrNotFound", err)
	}
}
