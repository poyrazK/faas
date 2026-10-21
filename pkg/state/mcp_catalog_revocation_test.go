package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestOAuthRevocationPartialAcknowledgementAndLeaseRecovery(t *testing.T) {
	at := time.Now().UTC()
	root := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: "revision"}, OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{LocalState: "disconnected", History: []api.MCPCatalogOAuthEvent{}, Revocation: &api.MCPCatalogOAuthRevocation{ID: "job", State: "pending", NextAttemptAt: at, Config: &api.MCPCatalogObserverOAuthConfig{}, Ciphertext: []byte("sealed"), ClientCiphertext: []byte("client")}}}
	job := state.MCPCatalogObserverJob{Role: "default", Snapshot: state.CloneMCPCatalogMonitor(root)}
	if err := state.ChangeRevocation(&root, &job, "claim", at); err != nil {
		t.Fatal(err)
	}
	stale := job
	if err := state.ChangeRevocation(&root, &job, "refresh_acknowledged", at); err != nil {
		t.Fatal(err)
	}
	at = at.Add(time.Minute)
	recovered := state.MCPCatalogObserverJob{Role: "default", Snapshot: state.CloneMCPCatalogMonitor(root)}
	if err := state.ChangeRevocation(&root, &recovered, "claim", at); err != nil {
		t.Fatal(err)
	}
	if !recovered.Snapshot.OAuthLifecycle.Revocation.RefreshAcknowledged {
		t.Fatal("lost durable partial acknowledgement")
	}
	if err := state.ChangeRevocation(&root, &stale, "access_acknowledged", at); !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("expired worker committed", err)
	}
	if err := state.ChangeRevocation(&root, &recovered, "access_acknowledged", at); err != nil {
		t.Fatal(err)
	}
	r := root.OAuthLifecycle.Revocation
	if r.State != "provider_acknowledged" || r.Ciphertext != nil || r.ClientCiphertext != nil || r.Config != nil || r.AcknowledgedAt == nil {
		t.Fatal("completed job retained private grant")
	}
}
func TestOAuthRevocationRetryExhaustion(t *testing.T) {
	at := time.Now().UTC()
	root := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: "revision"}, OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{LocalState: "disconnected", History: []api.MCPCatalogOAuthEvent{}, Revocation: &api.MCPCatalogOAuthRevocation{ID: "job", State: "pending", NextAttemptAt: at, Ciphertext: []byte("sealed")}}}
	for i := 0; i < api.MCPCatalogOAuthRevocationMaxAttempts; i++ {
		job := state.MCPCatalogObserverJob{Role: "default", Snapshot: state.CloneMCPCatalogMonitor(root)}
		if err := state.ChangeRevocation(&root, &job, "claim", at); err != nil {
			t.Fatal(err)
		}
		if err := state.ChangeRevocation(&root, &job, "provider_unavailable", at); err != nil {
			t.Fatal(err)
		}
		at = root.OAuthLifecycle.Revocation.NextAttemptAt
	}
	if root.OAuthLifecycle.Revocation.State != "manual_required" || root.OAuthLifecycle.Revocation.Ciphertext == nil || state.RevocationDue(root, at) {
		t.Fatal("retry exhaustion failed")
	}
	for i := 0; i < 100; i++ {
		state.LifecycleEvent(&root, "renewed", at)
	}
	if len(root.OAuthLifecycle.History) != api.MCPCatalogOAuthMaxLifecycleHistory {
		t.Fatal("audit history not bounded")
	}
}
