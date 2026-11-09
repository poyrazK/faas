// adr: 20261105152500
package state_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemReviewedConfigRollback(t *testing.T) { testReviewedConfigRollback(t, state.NewMemStore()) }
func TestPgReviewedConfigRollback(t *testing.T) {
	testReviewedConfigRollback(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func testReviewedConfigRollback(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	env := lifecycleEnvironment(t, store, account, project, "staging")
	writer, history := store.(state.EnvBatchStore), store.(state.ConfigRevisionStore)
	restore, reader := store.(state.ReviewedConfigRollbackStore), store.(state.ConfigRollbackReceiptReader)
	var stagingReceiptID string
	for _, tc := range []struct{ scope, environmentID string }{{"default", ""}, {env.Slug, env.ID}} {
		t.Run(tc.scope, func(t *testing.T) {
			zero := int64(0)
			first, err := writer.ApplyEnvBatch(ctx, state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: tc.scope, Limits: api.MustLimitsFor(api.PlanPro), Request: api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"MODE": "before"}}})
			if err != nil {
				t.Fatal(err)
			}
			second, err := writer.ApplyEnvBatch(ctx, state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: tc.scope, Limits: api.MustLimitsFor(api.PlanPro), Request: api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &first.ConfigChange.Revision, Values: map[string]string{"MODE": "newer"}}})
			if err != nil {
				t.Fatal(err)
			}
			values, secrets, err := history.ConfigState(ctx, account.ID, app.ID, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			expected := first.ConfigChange.Revision
			proof := strings.Repeat("a", 64)
			req := state.ConfigRestore{Finish: state.ConfigChangeFinish{AccountID: account.ID, AppID: app.ID, Scope: tc.scope, Source: state.ConfigSourceRollback, RestoredRevision: first.ConfigChange.Revision, Now: time.Now().UTC()}, ExpectedVariables: values, ExpectedSecrets: secrets, Variables: []state.ConfigVariableRestore{{Key: "MODE", Value: "before"}}, ExpectedRevision: &expected, ExpectedAppID: app.ID, ExpectedEnvironmentID: tc.environmentID, RequestID: uuid.NewString(), ReceiptProof: proof, ReviewedGuard: true}
			if _, err := restore.ApplyReviewedConfigRollback(ctx, req); !errors.Is(err, state.ErrConfigRestoreChanged) {
				t.Fatalf("stale reviewed revision = %v", err)
			}
			current, _, err := history.ConfigState(ctx, account.ID, app.ID, tc.scope)
			if err != nil || current["MODE"] != "newer" {
				t.Fatalf("stale restore changed values: %v %v", current, err)
			}
			if _, err := reader.ConfigRollbackReceipt(ctx, account.ID, app.ID, tc.scope, req.RequestID, proof); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("failed write recorded a receipt: %v", err)
			}
			expected = second.ConfigChange.Revision
			if tc.environmentID != "" {
				wrong := req
				wrong.ExpectedEnvironmentID = uuid.NewString()
				if _, err := restore.ApplyReviewedConfigRollback(ctx, wrong); !errors.Is(err, state.ErrConfigRestoreChanged) {
					t.Fatalf("wrong environment = %v", err)
				}
			}
			done, err := restore.ApplyReviewedConfigRollback(ctx, req)
			if err != nil || done.Revision != expected+1 || done.RestoreGuard == nil {
				t.Fatalf("restore=%+v %v", done, err)
			}
			receipt, err := reader.ConfigRollbackReceipt(ctx, account.ID, app.ID, tc.scope, req.RequestID, proof)
			if err != nil || receipt.ConfigRevision != done.Revision || receipt.RestoredRevision != first.ConfigChange.Revision || !receipt.Changed {
				t.Fatalf("receipt=%+v %v", receipt, err)
			}
			if tc.scope == env.Slug {
				stagingReceiptID = req.RequestID
			}
			if _, err := reader.ConfigRollbackReceipt(ctx, uuid.NewString(), app.ID, tc.scope, req.RequestID, proof); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("other account=%v", err)
			}
			if _, err := reader.ConfigRollbackReceipt(ctx, account.ID, app.ID, tc.scope, req.RequestID, strings.Repeat("b", 64)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("wrong proof=%v", err)
			}
			if _, err := restore.ApplyReviewedConfigRollback(ctx, req); !errors.Is(err, state.ErrConfigRollbackRecorded) {
				t.Fatalf("replay=%v", err)
			}
			current, currentSecrets, err := history.ConfigState(ctx, account.ID, app.ID, tc.scope)
			if err != nil {
				t.Fatal(err)
			}
			noop := req
			noop.ExpectedRevision, noop.ExpectedVariables, noop.ExpectedSecrets = &done.Revision, current, currentSecrets
			noop.RequestID, noop.Variables = uuid.NewString(), nil
			unchanged, err := restore.ApplyReviewedConfigRollback(ctx, noop)
			if err != nil || unchanged.Revision != done.Revision {
				t.Fatalf("no-op rollback=%+v %v", unchanged, err)
			}
			receipt, err = reader.ConfigRollbackReceipt(ctx, account.ID, app.ID, tc.scope, noop.RequestID, proof)
			if err != nil || receipt.Changed || receipt.ConfigRevision != done.Revision {
				t.Fatalf("no-op receipt=%+v %v", receipt, err)
			}
		})
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, env.Slug); err != nil {
		t.Fatal(err)
	}
	replacement := lifecycleEnvironment(t, store, account, project, env.Slug)
	if replacement.ID == env.ID {
		t.Fatal("environment was not recreated")
	}
	if _, err := reader.ConfigRollbackReceipt(ctx, account.ID, app.ID, env.Slug, stagingReceiptID, strings.Repeat("a", 64)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recreated environment=%v", err)
	}
}
