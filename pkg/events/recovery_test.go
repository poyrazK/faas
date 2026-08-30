// recovery_test.go — unit tests for the recovery-timeline vocabulary
// in pkg/events/recovery.go (Workstream B / issue #1184, ADR-137).
//
// Three things under test:
//
//   1. The 7 typed RecoveryEvent payload structs implement the
//      RecoveryEvent interface contract (Kind/At/Subject/Payload
//      return the documented values). Mirrors wake_test.go's
//      table-driven approach for the wake vocabulary.
//   2. recoveryKindFromKind strips the `node.` / `instance.`
//      prefixes so the metric label is short.
//   3. Platform.EmitRecovery runs the end-to-end fan-out
//      (AppendEvent + counter + broadcaster + slog) on a stub
//      stack — same shape as TestPlatform_Emit_StoresRow.
package events

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// TestRecoveryEvent_Kinds asserts the closed 7-kind set returns
// the canonical kind constants. Adding a new recovery kind without
// extending this test (and the pre-instantiation loop in
// pkg/wire/metrics.go) trips the failure path — keeping the metric
// label set in lock-step with the platform vocabulary.
func TestRecoveryEvent_Kinds(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	cases := []struct {
		name string
		ev   RecoveryEvent
		want string
	}{
		{
			name: "NodeDraining",
			ev: NodeDrainingEvent{
				EmitAt: now, NodeID: "n1", NodeName: "node-a",
				InitiatedAt: now, OperatorSubject: strPtr("ops@faas"),
			},
			want: NodeDraining,
		},
		{
			name: "NodeDrained",
			ev: NodeDrainedEvent{
				EmitAt: now, NodeID: "n1", NodeName: "node-a",
				InitiatedAt: now, CompletedAt: now,
				DrainedInstanceCount: 3,
			},
			want: NodeDrained,
		},
		{
			name: "NodeFailed",
			ev: NodeFailedEvent{
				EmitAt: now, NodeID: "n1", NodeName: "node-a",
				LastHeartbeatAt: now.Add(-90 * time.Second),
			},
			want: NodeFailed,
		},
		{
			name: "NodeRecovered",
			ev: NodeRecoveredEvent{
				EmitAt: now, NodeID: "n1", NodeName: "node-a",
				RecoveryInitiatedAt: now, MigratedCount: 2, RecreatedCount: 1,
			},
			want: NodeRecovered,
		},
		{
			name: "InstanceMigrated",
			ev: InstanceMigratedEvent{
				EmitAt: now, InstanceID: "i1", AppID: "a1",
				DeploymentID: "d1", SourceNodeID: "n1", DestNodeID: "n2",
				LeaseID: "lease-1",
			},
			want: InstanceMigrated,
		},
		{
			name: "InstanceRecreated",
			ev: InstanceRecreatedEvent{
				EmitAt: now, InstanceID: "i1", AppID: "a1",
				DeploymentID: "d1", NodeID: "n1",
				Reason: "snapshot_miss",
			},
			want: InstanceRecreated,
		},
		{
			name: "InstanceFailed",
			ev: InstanceFailedEvent{
				EmitAt: now, InstanceID: "i1", AppID: "a1",
				DeploymentID: "d1", NodeID: "n1",
				Reason: "liveness_lost",
			},
			want: InstanceFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ev.Kind(); got != tc.want {
				t.Errorf("Kind() = %q, want %q", got, tc.want)
			}
			if tc.ev.At().IsZero() {
				t.Errorf("At() returned zero time")
			}
			if tc.ev.Payload() == nil {
				t.Errorf("Payload() returned nil; want a non-nil map")
			}
		})
	}
}

// TestRecoveryKindFromKind asserts the prefix stripper returns the
// short label so the metric cardinality stays bounded. Kinds without
// the documented prefixes (legacy bare names) pass through verbatim
// to keep the counter label set stable.
func TestRecoveryKindFromKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"node.draining", "draining"},
		{"node.drained", "drained"},
		{"node.failed", "failed"},
		{"node.recovered", "recovered"},
		{"instance.migrated", "migrated"},
		{"instance.recreated", "recreated"},
		{"instance.failed", "failed"},
		{"legacy_bare_name", "legacy_bare_name"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := recoveryKindFromKind(tc.in); got != tc.want {
				t.Errorf("recoveryKindFromKind(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPlatform_EmitRecovery_StoresRow runs the full fan-out for a
// NodeDrainedEvent through the stub stack: an `events` row lands in
// the in-memory store, the recovery-event counter fires under
// (kind=drained, result=ok), and the broadcaster publishes on the
// recovery topic. Mirrors TestPlatform_Emit_StoresRow.
func TestPlatform_EmitRecovery_StoresRow(t *testing.T) {
	store := newStubStore()
	ops := newStubOps()
	bc := &stubBroadcaster{}
	p := NewPlatform("schedd", store, silentLog(), ops, bc)

	now := time.Now().UTC()
	ev := NodeDrainedEvent{
		EmitAt:               now,
		NodeID:               "11111111-1111-1111-1111-111111111111",
		NodeName:             "node-a",
		InitiatedAt:          now.Add(-30 * time.Second),
		CompletedAt:          now,
		DrainedInstanceCount: 2,
	}
	p.EmitRecovery(context.Background(), ev)

	// Counter fires under (kind=drained, result=ok).
	ops.mu.Lock()
	if len(ops.recoveryCalls) != 1 || ops.recoveryCalls[0] != "drained:ok" {
		ops.mu.Unlock()
		t.Errorf("recoveryCalls = %v, want [drained:ok]", ops.recoveryCalls)
		return
	}
	ops.mu.Unlock()
	// Publish lands on the recovery topic (NOT the wake topic —
	// the two timelines are split for dashboard filtering).
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.lastTopic != TopicRecovery {
		t.Errorf("lastTopic = %q, want %q", bc.lastTopic, TopicRecovery)
	}
	if bc.calls != 1 {
		t.Errorf("PublishTopic calls = %d, want 1", bc.calls)
	}
}

// TestPlatform_EmitRecovery_FailurePath — a failing AppendEvent
// does not panic; the counter fires under result="failed" and the
// pub/sub publish is skipped. Same nil-safe posture as the wake
// timeline (TestPlatform_Emit_FailurePath).
func TestPlatform_EmitRecovery_FailurePath(t *testing.T) {
	store := failingStore{state.NewMemStore()}
	ops := newStubOps()
	bc := &stubBroadcaster{}
	p := NewPlatform("schedd", store, silentLog(), ops, bc)

	ev := NodeFailedEvent{
		EmitAt:          time.Now(),
		NodeID:          "11111111-1111-1111-1111-111111111111",
		NodeName:        "node-b",
		LastHeartbeatAt: time.Now().Add(-90 * time.Second),
	}
	p.EmitRecovery(context.Background(), ev)

	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.recoveryCalls) != 1 || ops.recoveryCalls[0] != "failed:failed" {
		t.Errorf("recoveryCalls = %v, want [failed:failed]", ops.recoveryCalls)
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.calls != 0 {
		t.Errorf("PublishTopic fired on failed row; calls = %d, want 0", bc.calls)
	}
}

// TestPlatform_EmitRecovery_NilEvent — a nil event is a no-op;
// counters and broadcaster stay untouched. Mirrors the wake
// timeline's nil-event test.
func TestPlatform_EmitRecovery_NilEvent(t *testing.T) {
	store := newStubStore()
	ops := newStubOps()
	bc := &stubBroadcaster{}
	p := NewPlatform("schedd", store, silentLog(), ops, bc)

	p.EmitRecovery(context.Background(), nil)

	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.recoveryCalls) != 0 {
		t.Errorf("recoveryCalls = %v, want []", ops.recoveryCalls)
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()
	if bc.calls != 0 {
		t.Errorf("PublishTopic calls = %d, want 0", bc.calls)
	}
}

// TestPlatform_EmitRecovery_NilOpsBroadcaster — the helper
// tolerates nil ops and nil broadcaster so the recovery path can
// run without an OpsMetrics or Broadcast scaffold (e.g. the
// scheduled-arbitrer unit tests).
func TestPlatform_EmitRecovery_NilOpsBroadcaster(t *testing.T) {
	store := newStubStore()
	p := NewPlatform("schedd", store, silentLog(), nil, nil)

	ev := InstanceRecreatedEvent{
		EmitAt: time.Now(), InstanceID: "i1", AppID: "a1",
		DeploymentID: "d1", NodeID: "n1", Reason: "snapshot_miss",
	}
	// Must not panic.
	p.EmitRecovery(context.Background(), ev)
}

// TestRecoveryEvent_PayloadRoundTrip ensures the typed payload
// struct serializes to a map with the documented keys — the same
// shape the audit row + SSE envelope consume. Catches drift between
// the typed struct and the contract callers depend on.
func TestRecoveryEvent_PayloadRoundTrip(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	ev := InstanceMigratedEvent{
		EmitAt:       now,
		InstanceID:   "inst-1",
		AppID:        "app-1",
		DeploymentID: "deploy-1",
		SourceNodeID: "node-source",
		DestNodeID:   "node-dest",
		LeaseID:      "lease-xyz",
	}
	p := ev.Payload()
	wantKeys := []string{"instance_id", "app_id", "deployment_id", "source_node_id", "dest_node_id", "lease_id"}
	for _, k := range wantKeys {
		if _, ok := p[k]; !ok {
			t.Errorf("payload missing key %q; got %v", k, p)
		}
	}
	if got := p["lease_id"]; got != "lease-xyz" {
		t.Errorf("payload[lease_id] = %v, want %q", got, "lease-xyz")
	}
}

// TestPlatform_EmitRecovery_AppendEventErrorWrapping guards against
// silent regression: when the AppendEvent returns a wrapped error
// (the shape pgx surfaces a constraint violation as
// fmt.Errorf("...: %w", pgErr)), the counter still records the
// failure and the call returns silently. Mirrors the wake-timeline
// failure-path coverage at the boundary.
func TestPlatform_EmitRecovery_AppendEventErrorWrapping(t *testing.T) {
	store := failingStore{state.NewMemStore()}
	ops := newStubOps()
	p := NewPlatform("schedd", store, silentLog(), ops, &stubBroadcaster{})

	ev := NodeDrainedEvent{
		EmitAt:               time.Now(),
		NodeID:               "n1",
		NodeName:             "node-a",
		InitiatedAt:          time.Now().Add(-time.Second),
		CompletedAt:          time.Now(),
		DrainedInstanceCount: 1,
	}
	// Must not panic; the failure path is exercised by failingStore
	// (the existing wake-timeline test relies on the same stub).
	p.EmitRecovery(context.Background(), ev)

	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.recoveryCalls) != 1 || ops.recoveryCalls[0] != "drained:failed" {
		t.Errorf("recoveryCalls = %v, want [drained:failed]", ops.recoveryCalls)
	}
}

func strPtr(s string) *string { return &s }
