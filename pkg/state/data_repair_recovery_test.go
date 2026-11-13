package state

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func saveRepairRecoveryFixture(t *testing.T, store repairTestStore, op Operation) {
	t.Helper()
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		s.operationMemoryLocked().operations[op.ID] = cloneOperation(op)
		s.mu.Unlock()
	case *PgStore:
		raw, err := operationRecordJSON(op)
		if err != nil {
			t.Fatal(err)
		}
		if err := sqlc.New().UpdateDataRepairOperation(t.Context(), s.pool, sqlc.UpdateDataRepairOperationParams{ID: mustPgUUID(op.ID), State: string(op.State), Record: raw, ExpiresAt: operationPGTime(&op.ExpiresAt)}); err != nil {
			t.Fatal(err)
		}
	}
}

func reservedRepairRecoveryFixture(t *testing.T, store repairTestStore) (Operation, DataRepairAdmission) {
	t.Helper()
	op := seedRepairHealthOperation(t, store, time.Now().UTC().Add(-365*24*time.Hour), api.DataRepairReplacing, false)
	if s, ok := store.(*MemStore); ok {
		s.mu.Lock()
		old := op.AccountID
		op.AccountID = uuid.MustParse(old).String()
		account := s.accounts[old]
		delete(s.accounts, old)
		account.ID = op.AccountID
		s.accounts[op.AccountID] = account
		app := s.apps[op.AppID]
		app.AccountID = op.AccountID
		s.apps[app.ID] = app
		tenant := s.platformTenants[op.PlatformTenantID]
		tenant.AccountID = op.AccountID
		s.platformTenants[tenant.ID] = tenant
		s.mu.Unlock()
	}
	op.State = api.OperationFailed
	op.DataRepair.Candidates = 1
	op.DataRepair.ReplacementOperationID = uuid.NewString()
	saveRepairRecoveryFixture(t, store, op)
	id := op.DataRepair.ReplacementOperationID
	in := DataRepairAdmission{AccountID: op.AccountID, AppID: op.AppID, ID: id, Binding: op.DataRepairBinding, Plan: api.OperationDataRepair{
		PlanID: id, PlanDigest: strings.Repeat("c", 64), SourceOperationID: op.DataRepair.SourceOperationID,
		Candidates: 1, PlanExpiresAt: time.Now().UTC().Add(-time.Hour), ReplacesPlanID: op.ID, ReplacesPlanDigest: op.DataRepair.PlanDigest,
	}}
	return op, in
}

func seedRecoveryDrafts(t *testing.T, store repairTestStore, parent Operation, count int, expired bool) {
	t.Helper()
	drafts := make([]Operation, 0, count)
	for range count {
		op := cloneOperation(parent)
		op.ID = uuid.NewString()
		op.DataRepairID, op.DataRepair.PlanID = op.ID, op.ID
		op.State, op.FailureCode, op.LatestSequence = api.OperationAccepted, "", 1
		op.DataRepair.Phase, op.DataRepair.ReplacementOperationID = api.DataRepairPrepared, ""
		if !expired {
			now := time.Now().UTC()
			op.CreatedAt, op.UpdatedAt = now, now
			op.ExpiresAt, op.EventExpiresAt = now.Add(30*24*time.Hour), now.Add(30*24*time.Hour)
			op.DataRepair.PlanExpiresAt, op.DataRepair.PhaseStartedAt = now.Add(15*time.Minute), now
		}
		drafts = append(drafts, op)
	}
	switch s := store.(type) {
	case *MemStore:
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, op := range drafts {
			s.operationMemoryLocked().operations[op.ID] = op
		}
	case *PgStore:
		tx, err := s.pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(t.Context()) }()
		for _, op := range drafts {
			raw, err := operationRecordJSON(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := sqlc.New().InsertDataRepairOperation(t.Context(), tx, sqlc.InsertDataRepairOperationParams{ID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(op.AppID), TenantID: mustPgUUID(op.PlatformTenantID), State: string(op.State), Record: raw, ExpiresAt: operationPGTime(&op.ExpiresAt), CreatedAt: operationPGTime(&op.CreatedAt)}); err != nil {
				t.Fatal(err)
			}
			if err := sqlc.New().InsertDataRepairExecution(t.Context(), tx, mustPgUUID(op.ID)); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDataRepairHistoricalReplacementRecovery(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store repairTestStore = NewMemStore()
			if backend == "postgres" {
				store = NewPgStore(pgtest.OpenMigrated(t))
			}
			for _, condition := range []string{"late response", "disabled plan", "full quota"} {
				t.Run(condition, func(t *testing.T) {
					parent, in := reservedRepairRecoveryFixture(t, store)
					if condition == "disabled plan" {
						if err := store.UpdateAccountPlan(t.Context(), parent.AccountID, api.PlanFree); err != nil {
							t.Fatal(err)
						}
					}
					if condition == "full quota" {
						seedRecoveryDrafts(t, store, parent, api.MustLimitsFor(api.PlanHobby).Operations.PendingPerAccount, false)
						if err := store.UpdateAccountPlan(t.Context(), parent.AccountID, api.PlanHobby); err != nil {
							t.Fatal(err)
						}
					}
					// An executable replacement still needs ordinary plan/quota admission.
					if condition != "late response" {
						live := in
						live.Plan.PlanExpiresAt = time.Now().UTC().Add(time.Minute)
						if _, _, err := store.CreateDataRepairOperation(t.Context(), live); err == nil {
							t.Fatal("live replacement bypassed admission")
						}
					}
					child, created, err := store.CreateDataRepairOperation(t.Context(), in)
					if err != nil || !created || child.State != api.OperationFailed || child.DataRepair.Phase != api.DataRepairFailed || child.DataRepair.Approval != nil || !child.DataRepair.PlanExpiresAt.Equal(in.Plan.PlanExpiresAt) || !child.ExpiresAt.After(time.Now()) {
						t.Fatalf("historical child lost identity, retention or authority fence: %+v %v", child.DataRepair, err)
					}
					finished, err := store.OperationByID(t.Context(), parent.AccountID, "", parent.ID)
					if err != nil || finished.DataRepair.Phase != api.DataRepairReplaced || !finished.ExpiresAt.After(time.Now()) || !finished.EventExpiresAt.After(time.Now()) || !finished.DataRepair.PlanExpiresAt.Equal(parent.DataRepair.PlanExpiresAt) {
						t.Fatalf("late parent completion unavailable or source expiry changed: %+v %v", finished.DataRepair, err)
					}
					replay, created, err := store.CreateDataRepairOperation(t.Context(), in)
					if err != nil || created || replay.ID != child.ID || !replay.ExpiresAt.Equal(child.ExpiresAt) || replay.LatestSequence != child.LatestSequence {
						t.Fatal("lost completion response was not replayable", err)
					}
					if _, err := store.AdvanceDataRepairOperation(t.Context(), child.AccountID, child.ID, DataRepairAdvance{ExpectedSequence: child.LatestSequence, Phase: api.DataRepairRehearsing}); !errors.Is(err, ErrConflict) {
						t.Fatal("historical child gained execution authority", err)
					}
					for {
						deleted, err := store.(OperationRetentionStore).PruneOperationState(t.Context(), finished.ExpiresAt.Add(time.Second), api.OperationRetentionPageMax)
						if err != nil {
							t.Fatal(err)
						}
						if deleted == 0 {
							break
						}
					}
					if _, err := store.OperationByID(t.Context(), parent.AccountID, "", parent.ID); !errors.Is(err, ErrNotFound) {
						t.Fatal("completed parent retained forever", err)
					}
				})
			}
		})
	}
}

func TestDataRepairExpiredDraftQuotaReclamation(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store repairTestStore = NewMemStore()
			if backend == "postgres" {
				store = NewPgStore(pgtest.OpenMigrated(t))
			}
			parent, in := reservedRepairRecoveryFixture(t, store)
			seedRecoveryDrafts(t, store, parent, api.MustLimitsFor(api.PlanHobby).Operations.PendingPerAccount, true)
			if err := store.UpdateAccountPlan(t.Context(), parent.AccountID, api.PlanHobby); err != nil {
				t.Fatal(err)
			}
			count, err := store.(OperationDiagnosticStore).OperationPendingCount(t.Context(), parent.AccountID)
			if err != nil || count != 0 {
				t.Fatal("expired drafts consume capacity before maintenance", count, err)
			}
			// One tiny cleanup batch cannot visit all 1,000 drafts. Admission must
			// already work without waiting for all historical records to be swept.
			if _, err := store.(OperationRetentionStore).PruneOperationState(t.Context(), time.Now().UTC(), 1); err != nil {
				t.Fatal(err)
			}
			in.Plan.PlanExpiresAt = time.Now().UTC().Add(time.Minute)
			child, created, err := store.CreateDataRepairOperation(t.Context(), in)
			if err != nil || !created || child.DataRepair.Phase != api.DataRepairPrepared {
				t.Fatal("expired drafts blocked fresh replacement", err)
			}
			count, err = store.(OperationDiagnosticStore).OperationPendingCount(t.Context(), parent.AccountID)
			if err != nil || count != 1 {
				t.Fatal("fresh replacement was not counted", count, err)
			}
		})
	}
}
