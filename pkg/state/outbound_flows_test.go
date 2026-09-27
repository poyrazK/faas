package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgOutboundFlowAttributionSurvivesDeploymentRemovalAndIPReuse(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	accountID, appID, deploymentID := seedLiveDeploy(t, s, ctx, "flow-owner", "flow-owner")
	nodeID := resolveDefaultLocal(t, ctx, s)
	if _, err := pool.Exec(ctx, `UPDATE compute_nodes SET public_ip = '203.0.113.10' WHERE id = $1`, nodeID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	makeEvent := func(instance string, at time.Time) state.OutboundFlowEvent {
		return state.OutboundFlowEvent{
			ID: uuid.NewString(), ObservedAt: at, NodeID: nodeID,
			InstanceID: instance, AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
			SourceIP: "10.100.0.5", SourcePort: 43210,
			DestinationIP: "198.51.100.20", DestinationPort: 443, Protocol: "tcp",
		}
	}
	first := makeEvent(uuid.NewString(), now.Add(-48*time.Hour))
	second := makeEvent(uuid.NewString(), now)
	if err := s.InsertOutboundFlowEvents(ctx, []state.OutboundFlowEvent{first, second}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// A retry after an uncertain client timeout must not duplicate evidence.
	if err := s.InsertOutboundFlowEvents(ctx, []state.OutboundFlowEvent{second}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, deploymentID); err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	var count int
	var digest, egress, provenance string
	if err := pool.QueryRow(ctx, `SELECT count(*), min(image_digest), min(host(egress_ip)), min(egress_ip_source) FROM outbound_flow_events WHERE account_id = $1`, accountID).Scan(&count, &digest, &egress, &provenance); err != nil {
		t.Fatal(err)
	}
	if count != 2 || digest != "sha256:abc" || egress != "203.0.113.10" || provenance != "node_public" {
		t.Fatalf("historical rows: count=%d digest=%q egress=%q provenance=%q", count, digest, egress, provenance)
	}
	deleted, err := s.DeleteOutboundFlowEventsBefore(ctx, now.Add(-24*time.Hour), 100)
	if err != nil || deleted != 1 {
		t.Fatalf("retention: deleted=%d err=%v", deleted, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID); err != nil {
		t.Fatalf("delete app: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID); err != nil {
		t.Fatalf("erase account: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_flow_events WHERE account_id = $1`, accountID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("account erasure left %d flow rows", count)
	}
}
