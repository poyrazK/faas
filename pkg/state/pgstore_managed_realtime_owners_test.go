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

func TestPgStoreManagedRealtimeOwnerPruneExpired(t *testing.T) {
	s, ctx := pgStore(t)
	accountID, appID, _ := seedLiveDeploy(t, s, ctx, "-managed-realtime-owner-prune")
	endpoint, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, pgManagedRealtimeEndpoint(accountID, appID), 10, 50)
	if err != nil {
		t.Fatalf("CreateManagedRealtimeEndpointIfUnderQuota: %v", err)
	}
	node, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name:               "rt-owner-prune-" + uuid.NewString(),
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
	for _, connectionID := range []string{"rt_expired_1", "rt_expired_2"} {
		if _, err := s.ClaimManagedRealtimeConnectionOwner(ctx, connectionID, endpoint.ID, node.ID, time.Millisecond); err != nil {
			t.Fatalf("claim %s: %v", connectionID, err)
		}
	}
	if _, err := s.ClaimManagedRealtimeConnectionOwner(ctx, "rt_live", endpoint.ID, node.ID, time.Minute); err != nil {
		t.Fatalf("claim live: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	removed, err := s.PruneExpiredManagedRealtimeConnectionOwners(ctx, 1)
	if err != nil || removed != 1 {
		t.Fatalf("prune limit=1 = (%d, %v), want (1, nil)", removed, err)
	}
	removed, err = s.PruneExpiredManagedRealtimeConnectionOwners(ctx, 10)
	if err != nil || removed != 1 {
		t.Fatalf("prune remainder = (%d, %v), want (1, nil)", removed, err)
	}
	if _, err := s.GetManagedRealtimeConnectionOwner(ctx, "rt_live", endpoint.ID); err != nil {
		t.Fatalf("live owner lookup = %v, want present", err)
	}
	if _, err := s.PruneExpiredManagedRealtimeConnectionOwners(ctx, 0); err == nil {
		t.Fatal("prune limit=0 unexpectedly succeeded")
	}
}
