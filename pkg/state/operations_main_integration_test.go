// adr: 521
// adr: 568
package state_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestMemQueueReplayPreservesOperationRecoveryFence(t *testing.T) {
	for _, operationID := range []string{"", "customer-operation"} {
		t.Run("operation="+operationID, func(t *testing.T) {
			s := state.NewMemStore()
			before := state.Invocation{ID: "execution", AccountID: "account", AppID: "app", OperationID: operationID,
				DeploymentScope: "staging", QueueBindingID: "binding", Source: state.InvocationQueue,
				State: state.InvocationDeadLetter, Attempts: 3, ReplayGeneration: 7, DueAt: time.Now().Add(-time.Hour)}
			(*s.Invocations())[before.ID] = before
			got, err := s.RetryQueueDeadLetter(t.Context(), before.AccountID, before.ID)
			if operationID != "" {
				if !errors.Is(err, state.ErrConflict) || !reflect.DeepEqual((*s.Invocations())[before.ID], before) {
					t.Fatalf("operator replay changed operation-owned execution: %+v %v", got, err)
				}
				return
			}
			if err != nil || got.State != state.InvocationPending || got.Attempts != 0 || got.ReplayGeneration != before.ReplayGeneration+1 ||
				got.DeploymentScope != before.DeploymentScope || got.QueueBindingID != before.QueueBindingID || got.LastReplayedAt == nil {
				t.Fatalf("ordinary queue replay lost identity or replay fencing: %+v %v", got, err)
			}
		})
	}
}

func TestInvocationSQLConversionPreservesOperationAndEnvironmentIdentity(t *testing.T) {
	id := uuid.New()
	row := sqlc.Invocation{OperationID: pgtype.UUID{Bytes: id, Valid: true}, DeploymentScope: "staging",
		QueueBindingID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, ReplayGeneration: 4}
	inv, err := state.InvocationFromSQL(row)
	if err != nil || inv.OperationID != id.String() || inv.DeploymentScope != row.DeploymentScope ||
		inv.QueueBindingID != uuid.UUID(row.QueueBindingID.Bytes).String() || inv.ReplayGeneration != row.ReplayGeneration {
		t.Fatalf("SQL conversion dropped trusted execution metadata: %+v %v", inv, err)
	}
}

func TestMemOperationRetentionDoesNotPromoteHeldEnvironmentRollback(t *testing.T) {
	s, app, old, tenant := state.MemOperationCodeFixture(t, false)
	op := state.AdmitMemOperationCode(t, s, app, old, tenant)
	next, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, ImageDigest: "sha256:next"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(t.Context(), next.ID); err != nil {
		t.Fatal(err)
	}
	state.ExpireMemOperationCode(t, s, op)
	(*s.Mu()).Lock()
	held := (*s.Deployments())[old.ID]
	held.EnvironmentWorkloadRuntime = `{}`
	(*s.Deployments())[old.ID] = held
	(*s.Mu()).Unlock()
	if target, err := s.AutoRollbackDeploymentsTx(t.Context(), app.ID, next.ID); err != nil || target != "" {
		t.Fatalf("automatic rollback promoted held retained code: %q %v", target, err)
	}
	current, err := s.DeploymentByID(t.Context(), next.ID)
	if err != nil || current.Status != state.DeployLive || current.TrafficPercent != 100 {
		t.Fatalf("held rollback target changed serving deployment: %+v %v", current, err)
	}
}
