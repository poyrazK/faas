// adr: 20261105140140
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemEnvBatchReceiptRecovery(t *testing.T) {
	testEnvBatchReceiptRecovery(t, state.NewMemStore())
}
func TestPgEnvBatchReceiptRecovery(t *testing.T) {
	testEnvBatchReceiptRecovery(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func testEnvBatchReceiptRecovery(t *testing.T, store state.Store) {
	t.Helper()
	ctx := t.Context()
	account, project, app := environmentLifecycleFixture(t, store)
	env := lifecycleEnvironment(t, store, account, project, "staging")
	writer, reader := store.(state.EnvBatchStore), store.(state.EnvBatchReceiptReader)
	var stagingRequestID string
	for _, tc := range []struct{ scope, envID string }{{"default", ""}, {"staging", env.ID}} {
		t.Run(tc.scope, func(t *testing.T) {
			zero := int64(0)
			expectedEnvironmentID := ""
			if tc.envID != "" {
				expectedEnvironmentID = uuid.MustParse(tc.envID).String()
			}
			change := state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: tc.scope, Limits: api.MustLimitsFor(api.PlanPro), Request: api.EnvBatchRequest{ExpectedAppID: uuid.MustParse(app.ID).String(), ExpectedEnvironmentID: expectedEnvironmentID, ReceiptProof: strings.Repeat("a", 64), RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"PUBLIC_MODE": "PRIVATE_VALUE"}}}
			if tc.scope == "staging" {
				stagingRequestID = change.Request.RequestID
			}
			wrongApp := change
			wrongApp.Request.ExpectedAppID = uuid.NewString()
			if _, err := writer.ApplyEnvBatch(ctx, wrongApp); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("app identity=%v", err)
			}
			if tc.envID != "" {
				wrongEnv := change
				wrongEnv.Request.ExpectedEnvironmentID = uuid.NewString()
				if _, err := writer.ApplyEnvBatch(ctx, wrongEnv); !errors.Is(err, state.ErrConflict) {
					t.Fatalf("environment identity=%v", err)
				}
			}
			committed, err := writer.ApplyEnvBatch(ctx, change)
			if err != nil {
				t.Fatal(err)
			}
			found, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, tc.scope, change.Request.RequestID, change.Request.ReceiptProof)
			if err != nil || !reflect.DeepEqual(committed, found) || found.EnvironmentID != expectedEnvironmentID {
				t.Fatalf("receipt=%+v err=%v", found, err)
			}
			data, _ := json.Marshal(found)
			if strings.Contains(string(data), "PRIVATE_VALUE") || strings.Contains(string(data), change.Request.ReceiptProof) {
				t.Fatal("receipt exposed value")
			}
			if _, err := reader.EnvBatchReceipt(ctx, uuid.NewString(), app.ID, tc.scope, change.Request.RequestID, change.Request.ReceiptProof); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("other account=%v", err)
			}
			found.UpdatedKeys[0] = "MUTATED"
			if _, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, tc.scope, change.Request.RequestID, strings.Repeat("b", 64)); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("wrong proof=%v", err)
			}
			if _, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, tc.scope, change.Request.RequestID, ""); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("missing proof=%v", err)
			}
			again, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, tc.scope, change.Request.RequestID, change.Request.ReceiptProof)
			if err != nil || !reflect.DeepEqual(again, committed) {
				t.Fatal("receipt was not isolated")
			}
			// A different writer who guesses an attempt ID cannot make an
			// unrelated receipt pass the original private proof.
			poison := change
			poison.Request.RequestID = uuid.NewString()
			poison.Request.ReceiptProof = ""
			poison.Request.ExpectedRevision = &committed.ConfigChange.Revision
			poison.Request.Values = map[string]string{"PUBLIC_MODE": "other"}
			if _, err := writer.ApplyEnvBatch(ctx, poison); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, tc.scope, poison.Request.RequestID, change.Request.ReceiptProof); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("unrelated receipt accepted original proof: %v", err)
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
	// No retained receipt can be attributed to the newly created environment.
	if _, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, env.Slug, stagingRequestID, strings.Repeat("a", 64)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recreated scope=%v", err)
	}
	legacy := lifecycleEnvironment(t, store, account, project, "legacy")
	zero := int64(0)
	old := state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: legacy.Slug, Limits: api.MustLimitsFor(api.PlanPro), Request: api.EnvBatchRequest{RequestID: uuid.NewString(), ExpectedRevision: &zero, Values: map[string]string{"MODE": "legacy"}}}
	if _, err := writer.ApplyEnvBatch(ctx, old); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.EnvBatchReceipt(ctx, account.ID, app.ID, legacy.Slug, old.Request.RequestID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("legacy named receipt lacked identity: %v", err)
	}
}
