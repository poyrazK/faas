// ADR-521: only an owning account can authorize a fenced recovery of uncertain effects.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationsHTTPAccountRecoveryBoundary(t *testing.T) {
	testOperationsHTTPAccountRecoveryBoundary(t, false)
}
func TestOperationsHTTPRecoveryReceiptBoundary(t *testing.T) {
	testOperationsHTTPAccountRecoveryBoundary(t, true)
}

func testOperationsHTTPAccountRecoveryBoundary(t *testing.T, receipt bool) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "recovery-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "recovery-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key := func(account string, scopes []string) string {
		t.Helper()
		plain, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(ctx, account, hash, "recovery", scopes); err != nil {
			t.Fatal(err)
		}
		return plain
	}
	ownerKey := key(account.ID, []string{api.ScopeDeployWrite})
	readKey := key(account.ID, []string{api.ScopeAppsRead})
	foreignKey := key(foreign.ID, api.ScopesAdminOnly)
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "recovery-export", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
			InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`), ProgressStages: []string{"generating"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	op, _, err := store.AdmitOperation(ctx, state.OperationAdmission{AccountID: account.ID, DefinitionID: def.ID, PlatformTenantID: tenant.ID, IdempotencyKey: "recovery-export", Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.ClaimInvocation(ctx, op.CurrentInvocationID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, inv.ID, "lost handler response", time.Second, 10, state.WithClaimAttempt(inv.Attempts)); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	if srv.operationsAdmissionEnabled {
		t.Fatal("test must exercise recovery with admission closed")
	}
	handler := srv.handler()
	path := "/v1/apps/" + app.Slug + "/operations/" + op.ID + "/recover"
	if receipt {
		path += "-receipt"
	}
	recovery := api.OperationRecoveryRequest{RecoveryID: "checked-export", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "verified provider ledger and output storage: no external effect exists"}
	call := func(bearer string, body api.OperationRecoveryRequest) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	missingEvidence := recovery
	missingEvidence.Evidence = ""
	stale := recovery
	stale.ExpectedGeneration = 9
	for _, test := range []struct {
		name, bearer string
		body         api.OperationRecoveryRequest
		status       int
	}{
		{"read-key", readKey, recovery, http.StatusForbidden},
		{"foreign-account", foreignKey, recovery, http.StatusNotFound},
		{"missing-evidence", ownerKey, missingEvidence, http.StatusBadRequest},
		{"stale-generation", ownerKey, stale, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := call(test.bearer, test.body)
			if w.Code != test.status {
				t.Fatalf("%d, want %d: %s", w.Code, test.status, w.Body.String())
			}
		})
	}
	w := call(ownerKey, recovery)
	if w.Code != http.StatusOK {
		t.Fatalf("account recovery: %d %s", w.Code, w.Body.String())
	}
	var got api.OperationResponse
	if receipt {
		var d api.OperationRecoveryDecision
		if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		if d.RecoveryID != recovery.RecoveryID || d.InvocationID == inv.ID || d.ExpectedGeneration != 1 || d.RequestFingerprint == "" {
			t.Fatal("invalid decision", d)
		}
		got = api.OperationResponse{ID: d.OperationID, State: d.State, Generation: d.Generation}
	} else if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	current, err := store.OperationByID(ctx, account.ID, tenant.ID, op.ID)
	if err != nil || got.ID != op.ID || got.Generation != 2 || got.State != api.OperationAccepted || current.CurrentInvocationID == inv.ID {
		t.Fatalf("recovery lost logical identity or execution fence: %+v %v", got, err)
	}
	if repeat := call(ownerKey, recovery); repeat.Code != http.StatusOK || repeat.Body.String() != w.Body.String() {
		t.Fatalf("recovery replay: %d %s", repeat.Code, repeat.Body.String())
	}
	acceptedRequest := recovery
	recovery.RecoveryID = "another-check"
	if stale := call(ownerKey, recovery); stale.Code != http.StatusConflict {
		t.Fatalf("stale generation created another execution: %d %s", stale.Code, stale.Body.String())
	}
	if err := store.CompleteKeyedInvocation(ctx, inv.ID, inv.Attempts, []byte(`{}`)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("abandoned execution replaced recovered work: %v", err)
	}
	if receipt {
		claimed, err := store.ClaimInvocation(ctx, current.CurrentInvocationID, "", 60)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteKeyedInvocation(ctx, claimed.ID, claimed.Attempts, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if repeated := call(ownerKey, acceptedRequest); repeated.Code != http.StatusOK || repeated.Body.String() != w.Body.String() {
			t.Fatal("advanced work changed receipt", repeated.Code, repeated.Body.String())
		}
		path = strings.TrimSuffix(path, "-receipt")
		repeated := call(ownerKey, acceptedRequest)
		var current api.OperationResponse
		if json.Unmarshal(repeated.Body.Bytes(), &current) != nil || current.State != api.OperationSucceeded || repeated.Code != http.StatusOK {
			t.Fatal("legacy response stopped reporting current state", repeated.Code, repeated.Body.String())
		}
	}

}
