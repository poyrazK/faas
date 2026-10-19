package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func seedRepairHealthOperation(t *testing.T, s repairTestStore, at time.Time, phase string, legacy bool) Operation {
	t.Helper()
	ctx := t.Context()
	a, err := s.CreateAccount(ctx, "health-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, App{ID: uuid.NewString(), AccountID: a.ID, Slug: "health-" + uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := s.CreatePlatformTenant(ctx, a.ID, "health", "Repair health", 100)
	if err != nil {
		t.Fatal(err)
	}
	source := Operation{AccountID: a.ID, AppID: app.ID, PlatformTenantID: tenant.ID, Scope: "production", OperationResponse: api.OperationResponse{State: api.OperationCancelled}}
	id := uuid.NewString()
	in := DataRepairAdmission{AccountID: a.ID, AppID: app.ID, ID: id, Binding: strings.Repeat("b", 64), Plan: api.OperationDataRepair{PlanID: id, PlanDigest: strings.Repeat("a", 64), SourceOperationID: uuid.NewString(), PlanExpiresAt: at.Add(24 * time.Hour)}}
	op, err := newDataRepairOperation(source, in, api.MustLimitsFor(api.PlanPro).Operations, at.Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	op.DataRepair.Phase, op.DataRepair.PhaseStartedAt, op.UpdatedAt = phase, at.Add(-3*time.Minute), at
	op.State = api.OperationRunning
	if phase == api.DataRepairUnknown {
		op.State, op.FailureCode = api.OperationRequiresReconciliation, "repair_outcome_unknown"
	}
	if phase == api.DataRepairSucceeded {
		op.State = api.OperationSucceeded
	}
	if legacy {
		op.DataRepair.PhaseStartedAt = time.Time{}
	}
	switch store := s.(type) {
	case *MemStore:
		store.mu.Lock()
		store.operationMemoryLocked().operations[op.ID] = op
		store.mu.Unlock()
	case *PgStore:
		tx, e := store.pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		b, e := operationRecordJSON(op)
		if e != nil {
			t.Fatal(e)
		}
		err = sqlc.New().InsertDataRepairOperation(ctx, tx, sqlc.InsertDataRepairOperationParams{ID: mustPgUUID(op.ID), AccountID: mustPgUUID(a.ID), AppID: mustPgUUID(app.ID), TenantID: mustPgUUID(tenant.ID), State: string(op.State), Record: b, ExpiresAt: operationPGTime(&op.ExpiresAt), CreatedAt: operationPGTime(&op.CreatedAt)})
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlc.New().InsertDataRepairExecution(ctx, tx, mustPgUUID(op.ID)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("unsupported fixture store")
	}
	return op
}

func TestDataRepairHealthApplyOutcomes(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var s repairTestStore = NewMemStore()
			if backend == "postgres" {
				s = NewPgStore(pgtest.OpenMigrated(t))
			}
			health := s.(DataRepairHealthStore)
			now := time.Now().UTC().Truncate(time.Microsecond)
			apply := seedRepairHealthOperation(t, s, now, api.DataRepairApplying, true)
			unknown := seedRepairHealthOperation(t, s, now, api.DataRepairUnknown, false)
			seedRepairHealthOperation(t, s, now, api.DataRepairSucceeded, false)
			h, err := health.DataRepairHealth(t.Context(), now, "", 1)
			if err != nil || len(h.Phases) != len(api.DataRepairHealthPhases()) || len(h.Items) != 1 || h.NextCursor == "" || h.HeldSlots != 0 || h.HeldMemoryMB != 0 {
				t.Fatalf("snapshot: %+v %v", h, err)
			}
			if h.Phases[4].Count != 1 || h.Phases[4].OldestAgeSeconds != 600 || h.Phases[5].Count != 1 || h.Phases[5].OldestAgeSeconds != 180 {
				t.Fatalf("legacy phase age or counts: %+v", h.Phases)
			}
			page, err := health.DataRepairHealth(t.Context(), now, h.NextCursor, 1)
			if err != nil || len(page.Items) != 1 || page.NextCursor != "" || page.Items[0].OperationID == h.Items[0].OperationID || page.Phases[4].Count != 1 {
				t.Fatalf("next page changed global summary: %+v %v", page, err)
			}
			for _, item := range append(h.Items, page.Items...) {
				if item.OperationID != apply.ID && item.OperationID != unknown.ID || item.CapacityHeld || !item.NeedsAttention || item.NodeStatus != "unassigned" || item.RecoveryGuidance == "" {
					t.Fatalf("unsafe outcome projection: %+v", item)
				}
			}
			h, err = health.DataRepairHealth(t.Context(), now, "", 0)
			if err != nil || len(h.Items) != 0 || h.NextCursor != "" || h.Phases[5].Count != 1 {
				t.Fatalf("summary-only observation: %+v %v", h, err)
			}
			for _, cursor := range []string{"bad", "t:00000000-0000-0000-0000-000000000000", "o:030E2012-E15A-4205-9EDE-D9796F2B7544"} {
				if _, err := health.DataRepairHealth(t.Context(), now, cursor, 1); !errors.Is(err, ErrInvalidArgument) {
					t.Errorf("invalid cursor accepted: %q %v", cursor, err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := health.DataRepairHealth(ctx, now, "", 1); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled read: %v", err)
			}
		})
	}
}

func TestDataRepairHealthScheduledReservations(t *testing.T) {
	s := NewPgStore(pgtest.OpenMigrated(t))
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Microsecond)
	want := map[string]api.DataRepairHealthItem{}
	for _, tc := range []struct {
		phase, lifecycle, status string
		heartbeat                time.Duration
		legacy, held, cancel     bool
	}{
		{phase: "queued", status: "unassigned", legacy: true},
		{phase: "running", lifecycle: "active", status: "available", held: true},
		{phase: "cleanup", lifecycle: "active", status: "heartbeat_stale", heartbeat: -5 * time.Minute, held: true, cancel: true},
		{phase: "running", lifecycle: "recovering", status: "available", held: true},
		{phase: "running", lifecycle: "unavailable", status: "unavailable", held: true},
		{phase: "cleanup", lifecycle: "retired", status: "retired", held: true},
		{phase: "cleanup", lifecycle: "active", status: "heartbeat_stale", heartbeat: time.Minute, held: true},
		{phase: "publishing", lifecycle: "active", status: "unassigned", heartbeat: -5 * time.Minute},
		{phase: "failed", lifecycle: "active", status: "unassigned"},
	} {
		op := seedRepairHealthOperation(t, s, now, api.DataRepairRehearsing, false)
		task := DataRepairRehearsal{ID: uuid.NewString(), OperationID: op.ID, AccountID: op.AccountID, Phase: tc.phase, CreatedAt: now.Add(-10 * time.Minute), UpdatedAt: now, PhaseStartedAt: now.Add(-3 * time.Minute), CancelRequested: tc.cancel, Binding: "private-executor-binding"}
		if tc.legacy {
			task.PhaseStartedAt = time.Time{}
		}
		var nodeID any
		var deadline any
		if tc.lifecycle != "" {
			node, err := s.CreateComputeNode(ctx, ComputeNode{Name: "health-" + uuid.NewString(), TargetURL: "unix:///run/faas/test-health.sock", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 4, AdmissionCeilingMB: 4096, VCPUBudget: 4, Lifecycle: NodeLifecycle(tc.lifecycle)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.pool.Exec(ctx, "UPDATE compute_nodes SET last_heartbeat_at=$2 WHERE id=$1", node.ID, now.Add(tc.heartbeat)); err != nil {
				t.Fatal(err)
			}
			nodeID, deadline = node.ID, now.Add(time.Minute)
			task.Frame = &core.RehearsalFrame{NodeID: node.ID, Incarnation: "private-native-incarnation"}
		}
		if tc.phase == "cleanup" {
			task.FailureCode = "repair_cleanup_unconfirmed"
		}
		b, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.pool.Exec(ctx, "INSERT INTO data_repair_rehearsals(id,operation_id,account_id,node_id,deadline,phase,cancel_requested,record,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)", task.ID, op.ID, op.AccountID, nodeID, deadline, task.Phase, tc.cancel, b, task.CreatedAt); err != nil {
			t.Fatal(err)
		}
		if tc.phase != "failed" {
			age := int64(180)
			if tc.legacy {
				age = 600
			}
			want[task.ID] = api.DataRepairHealthItem{Phase: task.Phase, NodeStatus: tc.status, CapacityHeld: tc.held, AgeSeconds: age, CancelRequested: tc.cancel}
		}
	}
	cursor := ""
	seen := map[string]bool{}
	for {
		h, err := s.DataRepairHealth(ctx, now, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if h.HeldSlots != 6 || h.HeldMemoryMB != 6*(api.DataRepairSandboxMemoryMB+api.PerVMOverheadMB) || h.HeldVCPUs != 6 || h.HeldCPUMillicores != 6000 || h.UnavailableTasks != 4 || len(h.Phases) != len(api.DataRepairHealthPhases()) || h.Phases[0].Count != 1 || h.Phases[1].Count != 3 || h.Phases[2].Count != 3 || h.Phases[3].Count != 1 {
			t.Fatalf("global summary changed with page: %+v", h)
		}
		b, err := json.Marshal(h)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "private-") || strings.Contains(string(b), "snapshot_sha256") || strings.Contains(string(b), "frame") {
			t.Fatal("private executor/native metadata leaked")
		}
		for _, item := range h.Items {
			v, exists := want[item.TaskID]
			if !exists || seen[item.TaskID] || item.Phase != v.Phase || item.NodeStatus != v.NodeStatus || item.CapacityHeld != v.CapacityHeld || item.AgeSeconds != v.AgeSeconds || item.CancelRequested != v.CancelRequested || !item.NeedsAttention || item.RecoveryGuidance == "" || item.CapacityHeld && item.HeldMemoryMB != api.DataRepairSandboxMemoryMB+api.PerVMOverheadMB {
				t.Fatalf("task health: %+v, want %+v", item, v)
			}
			seen[item.TaskID] = true
		}
		if h.NextCursor == "" {
			break
		}
		cursor = h.NextCursor
	}
	if len(seen) != len(want) {
		t.Fatalf("pagination saw %d of %d tasks", len(seen), len(want))
	}
}

func TestDataRepairHealthClockSkew(t *testing.T) {
	now := time.Now().UTC()
	future := now.Add(time.Minute)
	h := finishDataRepairHealth(api.DataRepairHealth{Phases: []api.DataRepairPhaseHealth{{Phase: "queued", Count: 1, OldestPhaseStartedAt: &future}}, Items: []api.DataRepairHealthItem{{Phase: "queued", PhaseStartedAt: future}}}, now, 1)
	if h.Phases[0].OldestAgeSeconds != 0 || h.Items[0].AgeSeconds != 0 || h.Items[0].NeedsAttention {
		t.Fatal("future clock produced a negative age or false stall")
	}
}
