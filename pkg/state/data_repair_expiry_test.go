package state

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestDataRepairDraftExpirySafety(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store repairTestStore = NewMemStore()
			if backend == "postgres" {
				store = NewPgStore(pgtest.OpenMigrated(t))
			}
			for _, tc := range []struct {
				name, phase, rehearsal string
				approved, live, expire bool
			}{
				{name: "expired draft", phase: api.DataRepairPrepared, expire: true},
				{name: "live draft", phase: api.DataRepairPrepared, live: true},
				{name: "saved approval", phase: api.DataRepairPrepared, approved: true},
				{name: "queued rehearsal", phase: api.DataRepairPrepared, rehearsal: RepairRehearsalQueued},
				{name: "running rehearsal", phase: api.DataRepairPrepared, rehearsal: RepairRehearsalRunning},
				{name: "cleanup rehearsal", phase: api.DataRepairPrepared, rehearsal: RepairRehearsalCleanup},
				{name: "publishing rehearsal", phase: api.DataRepairPrepared, rehearsal: RepairRehearsalPublishing},
				{name: "awaiting approval", phase: api.DataRepairAwaitingApproval},
				{name: "approved", phase: api.DataRepairApproved, approved: true},
				{name: "applying", phase: api.DataRepairApplying},
				{name: "unknown outcome", phase: api.DataRepairUnknown},
				{name: "replacement reservation", phase: api.DataRepairReplacing},
				{name: "resolution reservation", phase: api.DataRepairResolving},
			} {
				t.Run(tc.name, func(t *testing.T) {
					now := time.Now().UTC().Truncate(time.Microsecond)
					op := seedRepairHealthOperation(t, store, now, tc.phase, false)
					op.DataRepair.PlanExpiresAt = now.Add(-time.Minute)
					op.DataRepair.RehearsalStatus = tc.rehearsal
					if tc.live {
						op.DataRepair.PlanExpiresAt = now.Add(time.Hour)
					}
					if tc.approved {
						op.DataRepair.Approval = &api.OperationApprovalDecision{Decision: "approve"}
					}
					saveRepairRecoveryFixture(t, store, op)
					pending, err := store.(OperationDiagnosticStore).OperationPendingCount(t.Context(), op.AccountID)
					wantPending := int64(1)
					if tc.expire {
						wantPending = 0
					}
					if err != nil || pending != wantPending {
						t.Fatal("wrong pending capacity", pending, err)
					}
					for range 2 {
						if _, err := store.(OperationRetentionStore).PruneOperationState(t.Context(), now, api.OperationRetentionPageMax); err != nil {
							t.Fatal(err)
						}
					}
					got, err := store.OperationByID(t.Context(), op.AccountID, "", op.ID)
					if err != nil || !got.DataRepair.PlanExpiresAt.Equal(op.DataRepair.PlanExpiresAt) || !got.ExpiresAt.Equal(op.ExpiresAt) || !got.EventExpiresAt.Equal(op.EventExpiresAt) {
						t.Fatal("expiry rewrote authority or ordinary retention", err)
					}
					if tc.expire {
						if got.State != api.OperationFailed || got.DataRepair.Phase != api.DataRepairFailed || got.FailureCode != "repair_plan_expired" || got.LatestSequence != op.LatestSequence+1 {
							t.Fatalf("draft did not expire once: %+v", got.OperationResponse)
						}
						events, err := store.OperationEvents(t.Context(), op.AccountID, "", op.ID, 0, 10)
						if err != nil || len(events.Events) != 1 || events.Events[0].Type != "failed" {
							t.Fatal("expiry failure event missing or duplicated", events, err)
						}
					} else if got.State != op.State || got.DataRepair.Phase != op.DataRepair.Phase || got.LatestSequence != op.LatestSequence {
						t.Fatal("active lifecycle changed during draft cleanup")
					}
				})
			}
		})
	}
}

func TestDataRepairDraftExpiryProtectsNativeTask(t *testing.T) {
	s := NewPgStore(pgtest.OpenMigrated(t))
	now := time.Now().UTC()
	op := seedRepairHealthOperation(t, s, now, api.DataRepairPrepared, false)
	op.DataRepair.PlanExpiresAt = now.Add(-time.Minute)
	saveRepairRecoveryFixture(t, s, op)
	// The durable native task remains authoritative even if the public operation
	// projection lacks its status after interrupted publication.
	task := DataRepairRehearsal{ID: uuid.NewString(), OperationID: op.ID, AccountID: op.AccountID, Phase: RepairRehearsalQueued}
	record, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlc.New().InsertDataRepairRehearsal(t.Context(), s.pool, sqlc.InsertDataRepairRehearsalParams{ID: mustPgUUID(task.ID), OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), Record: record}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneOperationState(t.Context(), now, api.OperationRetentionPageMax); err != nil {
		t.Fatal(err)
	}
	got, err := s.OperationByID(t.Context(), op.AccountID, "", op.ID)
	if err != nil || got.DataRepair.Phase != api.DataRepairPrepared || got.LatestSequence != op.LatestSequence {
		t.Fatal("native task expired through a stale public projection", err)
	}
	if count, err := s.OperationPendingCount(t.Context(), op.AccountID); err != nil || count != 1 {
		t.Fatal("native task lost its pending capacity", count, err)
	}
}

func TestDataRepairDraftExpirySkipsLocked(t *testing.T) {
	s := NewPgStore(pgtest.OpenMigrated(t))
	now := time.Now().UTC()
	locked := seedRepairHealthOperation(t, s, now.Add(-25*time.Hour), api.DataRepairPrepared, false)
	other := seedRepairHealthOperation(t, s, now.Add(-25*time.Hour), api.DataRepairPrepared, false)
	tx, err := s.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := sqlc.New().LockDataRepairOperation(t.Context(), tx, sqlc.LockDataRepairOperationParams{ID: mustPgUUID(locked.ID), AccountID: mustPgUUID(locked.AccountID)}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := s.ExpireDataRepairDrafts(ctx, now, 1); err != nil {
		t.Fatal("expiry blocked behind foreground work", err)
	}
	for _, op := range []Operation{locked, other} {
		got, err := s.OperationByID(t.Context(), op.AccountID, "", op.ID)
		want := api.DataRepairPrepared
		if op.ID == other.ID {
			want = api.DataRepairFailed
		}
		if err != nil || got.DataRepair.Phase != want {
			t.Fatal("locked expiry did not skip to the available draft", want, err)
		}
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.ExpireDataRepairDrafts(t.Context(), now, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.OperationByID(t.Context(), locked.AccountID, "", locked.ID)
	if err != nil || got.DataRepair.Phase != api.DataRepairFailed {
		t.Fatal("unlocked draft was never expired", err)
	}
}

func TestDataRepairExpiredDraftReplacementKeepsQuota(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store repairTestStore = NewMemStore()
			if backend == "postgres" {
				store = NewPgStore(pgtest.OpenMigrated(t))
			}
			parent, in := reservedRepairRecoveryFixture(t, store)
			parent.State, parent.DataRepair.Phase = api.OperationAccepted, api.DataRepairPrepared
			parent.DataRepair.ReplacementOperationID = ""
			saveRepairRecoveryFixture(t, store, parent)
			seedRecoveryDrafts(t, store, parent, api.MustLimitsFor(api.PlanHobby).Operations.PendingPerAccount, false)
			if err := store.UpdateAccountPlan(t.Context(), parent.AccountID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			reserved, err := store.AdvanceDataRepairOperation(t.Context(), parent.AccountID, parent.ID, DataRepairAdvance{ExpectedSequence: parent.LatestSequence, Phase: api.DataRepairReplacing, ReplacementOperationID: in.ID})
			if err != nil || !reserved.State.Terminal() {
				t.Fatal("expired parent resurrected a pending reservation", err)
			}
			in.Plan.PlanExpiresAt = time.Now().UTC().Add(time.Minute)
			if _, _, err := store.CreateDataRepairOperation(t.Context(), in); err == nil {
				t.Fatal("expired parent let a fresh child exceed quota")
			}
			got, err := store.OperationByID(t.Context(), parent.AccountID, "", parent.ID)
			if err != nil || got.DataRepair.Phase != api.DataRepairReplacing || got.DataRepair.ReplacementOperationID != in.ID {
				t.Fatal("rejected admission lost the reserved identity", err)
			}
		})
	}
}
