// adr: 521 — observational diagnostics never grant admission or repeat work.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func testOperationDoctorHTTP(t *testing.T, srv *server, store operationOperatorTestStore, acct state.Account, dep state.Deployment, tenant state.PlatformTenant, hook state.AppWebhook, read, foreign, customer string, call func(string, string, string, any) *httptest.ResponseRecorder) {
	t.Helper()
	counter := store.(state.OperationDiagnosticStore)
	before, err := counter.OperationPendingCount(t.Context(), acct.ID)
	if err != nil || before != 3 {
		t.Fatalf("pending fixture = %d: %v", before, err)
	}
	base := "/v1/apps/operator-exports/deployments/" + dep.ID + "/operation-doctor"
	path := base + "?tenant_id=" + tenant.ID + "&name=export"
	get := func() api.OperationDoctorResponse {
		t.Helper()
		w := call("GET", path, read, nil)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("doctor HTTP %d: %s", w.Code, w.Body.String())
		}
		for _, secret := range []string{hook.TargetURL, string(hook.SecretSealed), "input_schema", "output_schema", "platform_tenant_ids", "preview.json"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatal("diagnostics disclosed private contents", secret)
			}
		}
		var r api.OperationDoctorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.AppID != dep.AppID || r.DeploymentID != dep.ID || r.PlatformTenantID != tenant.ID || r.Scope != dep.Scope || r.ObservedAt.IsZero() || r.ObservationScope != "responding_api_node" || r.SubmissionState != r.ObservedSubmissionState() {
			t.Fatal("invalid projection", r)
		}
		return r
	}
	check := func(r api.OperationDoctorResponse, code, status, impact string) {
		t.Helper()
		for _, c := range r.Checks {
			if c.Code == code {
				if c.Status != status || c.Impact != impact {
					t.Fatal("misclassified diagnostic", c)
				}
				return
			}
		}
		t.Fatalf("missing check %s: %+v", code, r.Checks)
	}
	r := get()
	if r.SubmissionState != "blocked" {
		t.Fatal("closed default reported eligible")
	}
	check(r, "preview_not_configured", "blocked", "submission")
	check(r, "workload_trust_missing", "blocked", "submission")
	check(r, "result_storage_missing", "blocked", "submission")
	check(r, "completion_destination_configured", "configured", "delivery")
	for _, c := range r.Checks {
		if c.Check == "pending_capacity" && (c.Observed == nil || *c.Observed != before || c.Limit == nil || *c.Limit != int64(api.MustLimitsFor(acct.Plan).Operations.PendingPerAccount)) {
			t.Fatal("quota count differs from admission", c)
		}
	}
	for _, item := range []struct {
		path, bearer string
		code         int
	}{{path, foreign, 404}, {path, customer, 403}, {base, read, 400}, {path + "&tenant_id=" + tenant.ID, read, 400}, {path + "&scope=staging", read, 400}, {base + "?tenant_id=" + uuid.NewString(), read, 404}, {base + "?tenant_id=" + tenant.ID + "&name=missing", read, 404}, {base + "?tenant_id=" + tenant.ID + "&name=", read, 400}, {base + "?tenant_id=" + tenant.ID + ";name=export", read, 400}} {
		w := call("GET", item.path, item.bearer, nil)
		if w.Code != item.code {
			t.Fatalf("doctor selector/ownership %s HTTP %d want %d", item.path, w.Code, item.code)
		}
	}
	oldGate, oldVerifier, oldStorage := srv.operationsPreview, srv.operationsWorkloadVerifier, srv.operationArtifactStorage
	defer func() {
		srv.operationsPreview, srv.operationsWorkloadVerifier, srv.operationArtifactStorage = oldGate, oldVerifier, oldStorage
	}()
	policyPath := filepath.Join(t.TempDir(), "preview.json")
	gate, _ := operations.NewPreviewAdmission(policyPath)
	srv.operationsPreview = gate
	// Presence is configuration evidence only; no JWT/storage I/O is claimed.
	srv.operationsWorkloadVerifier = &workloadidentity.Verifier{}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv.operationArtifactStorage = backend
	now := time.Now().UTC()
	policy := operations.PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute), Cohorts: []operations.PreviewCohort{{AccountID: uuid.MustParse(acct.ID).String(), AppID: uuid.MustParse(dep.AppID).String(), Scope: dep.Scope, PlatformTenantIDs: []string{tenant.ID}}}}
	writePolicy := func(p operations.PreviewPolicy) {
		t.Helper()
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(policyPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy(policy)
	r = get()
	if r.SubmissionState != "eligible" {
		t.Fatal("configured cohort blocked", r)
	}
	for _, code := range []string{"runtime_reporting_unverified", "result_storage_io_unverified", "native_lifecycle_unverified", "fleet_rollback_unverified", "gateway_admission_unverified"} {
		check(r, code, "unknown", "qualification")
	}
	check(r, "workload_trust_configured", "configured", "submission")
	check(r, "definition_revision_observed", "observed", "submission")
	// A disabled notification does not block or repeat successful business work.
	no := false
	if _, err := store.UpdateAppWebhook(t.Context(), hook.ID, state.UpdateAppWebhookParams{Enabled: &no}); err != nil {
		t.Fatal(err)
	}
	r = get()
	check(r, "completion_destination_disabled", "warning", "delivery")
	if r.SubmissionState != "eligible" {
		t.Fatal("delivery fault blocked execution")
	}
	yes := true
	if _, err := store.UpdateAppWebhook(t.Context(), hook.ID, state.UpdateAppWebhookParams{Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetPlatformTenantStatus(t.Context(), acct.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	r = get()
	check(r, "tenant_suspended", "blocked", "submission")
	if _, err := store.SetPlatformTenantStatus(t.Context(), acct.ID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	policy.ExpiresAt = now.Add(-time.Second)
	writePolicy(policy)
	r = get()
	check(r, "preview_expired", "blocked", "submission")
	if srv.operationTenantAdmission(acct.ID, dep.AppID, dep.Scope, tenant.ID) {
		t.Fatal("doctor altered closed policy")
	}
	if err := os.WriteFile(policyPath, []byte("invalid-policy-containing-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	r = get()
	check(r, "preview_policy_unavailable", "blocked", "submission")
	writePolicy(policy)
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	r = get()
	check(r, "plan_not_allowed", "blocked", "submission")
	if err := store.UpdateAccountPlan(t.Context(), acct.ID, acct.Plan); err != nil {
		t.Fatal(err)
	}
	after, err := counter.OperationPendingCount(t.Context(), acct.ID)
	if err != nil || after != before {
		t.Fatal("doctor created or settled business work", after, err)
	}
	if _, err := counter.OperationPendingCount(t.Context(), uuid.NewString()); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unknown account count parity", err)
	}
	// Test-only override verifies actual-limit exhaustion and unreadable count.
	for _, tc := range []struct {
		count        int64
		err          error
		status, code string
	}{{int64(api.MustLimitsFor(acct.Plan).Operations.PendingPerAccount), nil, "blocked", "pending_capacity_exhausted"}, {0, errors.New("private database address"), "unknown", "pending_capacity_unavailable"}} {
		original := srv.store
		srv.store = operationDoctorCounterFixture{operationOperatorTestStore: store, count: tc.count, err: tc.err}
		c := srv.observeOperationPending(t.Context(), acct.ID, int64(api.MustLimitsFor(acct.Plan).Operations.PendingPerAccount))
		srv.store = original
		if c.Status != tc.status || c.Code != tc.code || strings.Contains(c.Message, "private database") {
			t.Fatal("quota uncertainty misclassified", c)
		}
	}
}

type operationDoctorCounterFixture struct {
	operationOperatorTestStore
	count int64
	err   error
}

func (s operationDoctorCounterFixture) OperationPendingCount(context.Context, string) (int64, error) {
	return s.count, s.err
}

func TestOperationDoctorUnusableContractAndReleasePins(t *testing.T) {
	// adr: 521 — do not infer eligibility from unreadable or mismatched pins.
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "doctor-pins@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "doctor-pins", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.PutOperationDefinition(t.Context(), state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, ProgressStages: []string{"generating"}, InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	changed := d
	changed.Revision = "changed"
	checks := srv.observeOperationDefinition(t.Context(), acct, dep, changed)
	if checks[0].Status != "blocked" || checks[0].Code != "definition_contract_unusable" {
		t.Fatal("invalid revision observed as usable", checks)
	}
	changed = d
	changed.ReleaseID = uuid.NewString()
	checks = srv.observeOperationDefinition(t.Context(), acct, dep, changed)
	if checks[1].Status != "blocked" || checks[1].Code != "release_pin_unavailable" {
		t.Fatal("missing release observed as usable", checks)
	}
	changed = d
	changed.Spec.CompletionWebhookID = uuid.NewString()
	check := srv.observeOperationCompletion(t.Context(), acct.ID, app.ID, changed.Spec.CompletionWebhookID)
	if check.Impact != "delivery" || check.Status != "warning" {
		t.Fatal("missing completion conflated with business work", check)
	}
	r := api.OperationDoctorResponse{Checks: []api.OperationDoctorCheck{{Impact: "submission", Status: "unknown"}, {Impact: "delivery", Status: "warning"}}}
	if r.ObservedSubmissionState() != "unknown" {
		t.Fatal("unknown prerequisite eligible")
	}
	r.Checks = append(r.Checks, api.OperationDoctorCheck{Impact: "submission", Status: "blocked"})
	if r.ObservedSubmissionState() != "blocked" {
		t.Fatal("known blocker lost to unknown check")
	}
}
