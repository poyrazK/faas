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
		replyPort := uint16(50888)
		return state.OutboundFlowEvent{
			ID: uuid.NewString(), ObservedAt: at, NodeID: nodeID,
			InstanceID: instance, AccountID: accountID, AppID: appID, DeploymentID: deploymentID,
			SourceIP: "10.100.0.5", SourcePort: 43210,
			DestinationIP: "198.51.100.20", DestinationPort: 443, Protocol: "tcp",
			ReplyDestinationIP: "192.0.2.10", ReplyDestinationPort: &replyPort,
		}
	}
	first := makeEvent(uuid.NewString(), now.Add(-48*time.Hour))
	second := makeEvent(uuid.NewString(), now)
	if err := s.InsertOutboundFlowEvents(ctx, []state.OutboundFlowEvent{first, second}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	otherAccountID, otherAppID, otherDeploymentID := seedLiveDeploy(t, s, ctx, "reused-flow-owner", "reused-flow-owner")
	other := makeEvent(uuid.NewString(), now.Add(time.Second))
	other.AccountID, other.AppID, other.DeploymentID = otherAccountID, otherAppID, otherDeploymentID
	if err := s.InsertOutboundFlowEvents(ctx, []state.OutboundFlowEvent{other}); err != nil {
		t.Fatalf("insert reused IP for another account: %v", err)
	}
	// A retry after an uncertain client timeout must not duplicate evidence.
	if err := s.InsertOutboundFlowEvents(ctx, []state.OutboundFlowEvent{second}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, deploymentID); err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	var count int
	var digest, egress, provenance, replyDestination string
	var replyPort int
	if err := pool.QueryRow(ctx, `SELECT count(*), min(image_digest), min(host(egress_ip)), min(egress_ip_source), min(host(reply_destination_ip)), min(reply_destination_port) FROM outbound_flow_events WHERE account_id = $1`, accountID).Scan(&count, &digest, &egress, &provenance, &replyDestination, &replyPort); err != nil {
		t.Fatal(err)
	}
	if count != 2 || digest != "sha256:abc" || egress != "203.0.113.10" || provenance != "node_public" || replyDestination != "192.0.2.10" || replyPort != 50888 {
		t.Fatalf("historical rows: count=%d digest=%q egress=%q provenance=%q reply=%s:%d", count, digest, egress, provenance, replyDestination, replyPort)
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_flow_events WHERE account_id = $1 AND source_ip = '10.100.0.5'::inet`, otherAccountID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("first account erasure removed another account's reused-IP evidence: count=%d", count)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, otherDeploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, otherAppID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, otherAccountID); err != nil {
		t.Fatal(err)
	}
}

func TestPgOutboundFlowCaptureSamplesRetainKnownLossAndExpire(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	nodeID := resolveDefaultLocal(t, ctx, s)
	sessionID := uuid.NewString()
	now := time.Now().UTC()
	makeSample := func(at time.Time, listening bool, dropped int64) state.OutboundFlowCaptureSample {
		return state.OutboundFlowCaptureSample{
			ID: uuid.NewString(), SessionID: sessionID, NodeID: nodeID,
			SampledAt: at, Listening: listening, Reason: "heartbeat",
			QueueDroppedTotal: dropped, UnattributedTotal: dropped,
		}
	}
	old := makeSample(now.Add(-48*time.Hour), true, 0)
	recent := makeSample(now, false, 4)
	for _, sample := range []state.OutboundFlowCaptureSample{old, recent, recent} {
		if err := s.InsertOutboundFlowCaptureSample(ctx, sample); err != nil {
			t.Fatalf("insert coverage sample: %v", err)
		}
	}
	var count int
	var dropped, unattributed int64
	if err := pool.QueryRow(ctx, `SELECT count(*), max(queue_dropped_total), max(unattributed_total) FROM outbound_flow_capture_samples WHERE session_id = $1`, sessionID).Scan(&count, &dropped, &unattributed); err != nil {
		t.Fatal(err)
	}
	if count != 2 || dropped != 4 || unattributed != 4 {
		t.Fatalf("coverage rows=%d lost=%d unattributed=%d", count, dropped, unattributed)
	}
	deleted, err := s.DeleteOutboundFlowCaptureSamplesBefore(ctx, now.Add(-24*time.Hour), 100)
	if err != nil || deleted != 1 {
		t.Fatalf("coverage retention: deleted=%d err=%v", deleted, err)
	}
}

func TestPgOutboundFlowIPLeasesSurviveInstanceDeletionAndReuse(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	firstAccount, firstApp, firstDeployment := seedLiveDeploy(t, s, ctx, "flow-lease-first", "flow-lease-first")
	secondAccount, secondApp, secondDeployment := seedLiveDeploy(t, s, ctx, "flow-lease-second", "flow-lease-second")
	nodeID := resolveDefaultLocal(t, ctx, s)
	first, err := s.CreateInstance(ctx, firstApp, firstDeployment, string(state.StateColdBooting), 512, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstanceRuntime(ctx, first.ID, "fc-first", "10.100.0.5", 20001); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := pool.QueryRow(ctx, `SELECT account_id::text FROM outbound_flow_ip_leases WHERE instance_id = $1 AND active_until IS NULL`, first.ID).Scan(&owner); err != nil || owner != firstAccount {
		t.Fatalf("first open lease owner=%q err=%v", owner, err)
	}
	if err := s.UpdateInstanceStateIf(ctx, first.ID, string(state.StateColdBooting), string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateIf(ctx, first.ID, string(state.StateRunning), string(state.StateDraining)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT account_id::text FROM outbound_flow_ip_leases WHERE instance_id = $1 AND active_until IS NULL`, first.ID).Scan(&owner); err != nil || owner != firstAccount {
		t.Fatalf("draining lease owner=%q err=%v", owner, err)
	}
	if err := s.UpdateInstanceStateIf(ctx, first.ID, string(state.StateDraining), string(state.StateStopped)); err != nil {
		t.Fatal(err)
	}
	// STOPPED retains host_ip. A state-only retry is not a new network lease
	// until SetInstanceRuntime publishes the next boot's identity.
	if err := s.UpdateInstanceStateIf(ctx, first.ID, string(state.StateStopped), string(state.StateColdBooting)); err != nil {
		t.Fatal(err)
	}
	var openCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbound_flow_ip_leases WHERE instance_id = $1 AND active_until IS NULL`, first.ID).Scan(&openCount); err != nil || openCount != 0 {
		t.Fatalf("stale host IP reopened a lease: count=%d err=%v", openCount, err)
	}
	if err := s.DeleteInstance(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateInstance(ctx, secondApp, secondDeployment, string(state.StateColdBooting), 512, nodeID, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInstanceRuntime(ctx, second.ID, "fc-second", "10.100.0.5", 20002); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT instance_id::text, account_id::text, active_until IS NOT NULL
		FROM outbound_flow_ip_leases WHERE node_id = $1 AND host_ip = '10.100.0.5'::inet ORDER BY active_from, id`, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type lease struct {
		instance, account string
		closed            bool
	}
	var leases []lease
	for rows.Next() {
		var row lease
		if err := rows.Scan(&row.instance, &row.account, &row.closed); err != nil {
			t.Fatal(err)
		}
		leases = append(leases, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(leases) != 2 || leases[0] != (lease{first.ID, firstAccount, true}) || leases[1] != (lease{second.ID, secondAccount, false}) {
		t.Fatalf("historical leases = %#v", leases)
	}
	if _, err := pool.Exec(ctx, `UPDATE outbound_flow_ip_leases SET active_from = now() - interval '48 hours', active_until = now() - interval '47 hours' WHERE instance_id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	removed, err := s.DeleteOutboundFlowIPLeasesBefore(ctx, time.Now().Add(-24*time.Hour), 100)
	if err != nil || removed != 1 {
		t.Fatalf("lease retention removed=%d err=%v", removed, err)
	}
}
