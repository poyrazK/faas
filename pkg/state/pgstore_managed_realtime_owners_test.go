//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreManagedRealtimeOwnerLeaseLifecycle(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-managed-realtime-owner")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "rt-owner-" + uuid.NewString(),
		TargetURL:          "unix:///run/faas/vmmd.sock",
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     16,
		AdmissionCeilingMB: 4096,
		VCPUBudget:         160,
		Active:             true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	defaultLocalID := resolveDefaultLocal(t, ctx, s)

	claimed, err := s.ClaimManagedRealtimeConnectionOwner(ctx, "rt_conn_1", endpoint.ID, node.ID, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.LeaseToken == "" || claimed.NodeID != node.ID || claimed.EndpointID != endpoint.ID {
		t.Fatalf("claim = %+v", claimed)
	}

	if _, err := s.ClaimManagedRealtimeConnectionOwner(ctx, claimed.ConnectionID, endpoint.ID, defaultLocalID, time.Minute); !errors.Is(err, state.ErrManagedRealtimeOwnerConflict) {
		t.Fatalf("competing claim = %v, want ErrManagedRealtimeOwnerConflict", err)
	}
	got, err := s.GetManagedRealtimeConnectionOwner(ctx, claimed.ConnectionID, endpoint.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LeaseToken != claimed.LeaseToken {
		t.Fatalf("get token=%q, want %q", got.LeaseToken, claimed.LeaseToken)
	}

	renewed, err := s.RenewManagedRealtimeConnectionOwner(ctx, claimed.ConnectionID, endpoint.ID, claimed.LeaseToken, time.Minute)
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	if !renewed.LeaseExpiresAt.After(claimed.LeaseExpiresAt) {
		t.Fatalf("renew expiry=%s, want after %s", renewed.LeaseExpiresAt, claimed.LeaseExpiresAt)
	}
	if _, err := s.RenewManagedRealtimeConnectionOwner(ctx, claimed.ConnectionID, endpoint.ID, "wrong-token", time.Minute); !errors.Is(err, state.ErrManagedRealtimeOwnerConflict) {
		t.Fatalf("wrong-token renew = %v, want conflict", err)
	}
	if err := s.ReleaseManagedRealtimeConnectionOwner(ctx, claimed.ConnectionID, claimed.LeaseToken); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := s.GetManagedRealtimeConnectionOwner(ctx, claimed.ConnectionID, endpoint.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("get after release = %v, want ErrNotFound", err)
	}
}
