// adr: 521 — diagnostic eligibility is not runtime qualification or admission.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type customerOperationDoctorFixture struct {
	report api.OperationDoctorResponse
	err    error
	calls  int
}

func (f *customerOperationDoctorFixture) GetOperationDoctor(_ context.Context, app, deployment, tenant, name string) (api.OperationDoctorResponse, error) {
	f.calls++
	return f.report, f.err
}
func doctorCLIFixture() (customerOperationDoctorCommand, api.OperationDoctorResponse) {
	c := customerOperationDoctorCommand{app: "exports", deployment: developerDefinitionID, tenant: developerOperationID, name: "export"}
	r := api.OperationDoctorResponse{AppID: developerDefinitionID, Scope: "production", DeploymentID: c.deployment, PlatformTenantID: c.tenant, Plan: api.PlanPro, ObservedAt: time.Now().UTC(), ObservationScope: "responding_api_node", Checks: []api.OperationDoctorCheck{{Check: "preview", Status: "observed", Impact: "submission", Code: "preview_cohort_observed", Message: "Preview binding observed."}, {Check: "completion_destination", Status: "warning", Impact: "delivery", Code: "completion_destination_disabled", Message: "Webhook disabled.", Remediation: "Repair notification; preserve the existing business result."}, {Check: "native_lifecycle", Status: "unknown", Impact: "qualification", Code: "native_lifecycle_unverified", Message: "Native lifecycle unverified."}}}
	for _, required := range []string{"tenant", "plan", "deployment_pin", "pending_capacity", "workload_trust", "result_storage", "definition_contract"} {
		r.Checks = append(r.Checks, api.OperationDoctorCheck{Check: required, Status: "observed", Impact: "submission", Code: required + "_observed", Message: "Observed."})
	}
	r.SubmissionState = r.ObservedSubmissionState()
	return c, r
}
func TestCustomerOperationDoctorReadOnlyWireAndSeparateOutcomes(t *testing.T) {
	c, r := doctorCLIFixture()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests++
		if req.Method != "GET" || req.URL.Path != "/v1/apps/exports/deployments/"+c.deployment+"/operation-doctor" || req.URL.Query().Get("tenant_id") != c.tenant || req.URL.Query().Get("name") != c.name || req.Header.Get("Authorization") != "Bearer owner-test" {
			t.Error("unexpected diagnostic wire request", req.Method, req.URL)
		}
		_ = json.NewEncoder(w).Encode(r)
	}))
	defer srv.Close()
	var output bytes.Buffer
	code, err := runCustomerOperationDoctor(t.Context(), api.NewClient(srv.URL, "owner-test").SetCompletionCache(nil), c, &output, true)
	if err != nil || code != 0 || requests != 1 || !strings.Contains(output.String(), "completion_destination_disabled") || !strings.Contains(output.String(), "native_lifecycle_unverified") {
		t.Fatal("read-only report lost outcome separation", code, err, output.String())
	}
}
func TestCustomerOperationDoctorExitStatesAndHumanRemediation(t *testing.T) {
	c, original := doctorCLIFixture()
	for _, state := range []string{"eligible", "blocked", "unknown"} {
		t.Run(state, func(t *testing.T) {
			r := original
			r.Checks = append([]api.OperationDoctorCheck{}, original.Checks...)
			switch state {
			case "blocked":
				r.Checks[0].Status = "blocked"
				r.Checks[0].Code = "preview_expired"
			case "unknown":
				r.Checks[0].Status = "unknown"
				r.Checks[0].Code = "pending_capacity_unavailable"
			}
			r.SubmissionState = r.ObservedSubmissionState()
			f := &customerOperationDoctorFixture{report: r}
			var out bytes.Buffer
			code, err := runCustomerOperationDoctor(t.Context(), f, c, &out, false)
			expected := map[string]int{"eligible": 0, "blocked": 1, "unknown": 3}[state]
			if code != expected || err != nil || f.calls != 1 || !strings.Contains(out.String(), "responding API node only") || !strings.Contains(out.String(), "Repair notification; preserve") {
				t.Fatal("doctor rendering/exit", code, err, out.String())
			}
		})
	}
}

func TestCustomerOperationDoctorExecutionEligibility(t *testing.T) {
	c, r := doctorCLIFixture()
	r.Checks = append(r.Checks, api.OperationDoctorCheck{Check: "execution_preview", Status: "blocked", Impact: "submission", Code: "preview_execution_kind_excluded", Message: "Job admission is closed in this cohort.", DefinitionID: developerDefinitionID, Name: "export", ExecutionKind: "job"})
	r.SubmissionState = r.ObservedSubmissionState()
	var out bytes.Buffer
	code, err := runCustomerOperationDoctor(t.Context(), &customerOperationDoctorFixture{report: r}, c, &out, false)
	if err != nil || code != 1 || !strings.Contains(out.String(), "execution_preview/export [job]") {
		t.Fatal("execution eligibility missing", code, err, out.String())
	}
	r.Checks[len(r.Checks)-1].ExecutionKind = "native"
	if err := validateCustomerOperationDoctor(r, c); err == nil {
		t.Fatal("unknown execution type accepted")
	}
	r.Checks[len(r.Checks)-1].ExecutionKind = ""
	r.Checks[len(r.Checks)-1].Status = "observed"
	r.SubmissionState = r.ObservedSubmissionState()
	if err := validateCustomerOperationDoctor(r, c); err == nil {
		t.Fatal("untyped execution grant accepted")
	}
}
func TestCustomerOperationDoctorRejectsInvalidSelectorsAndReports(t *testing.T) {
	c, r := doctorCLIFixture()
	good := []string{"--app", c.app, "--deployment", c.deployment, "--tenant", c.tenant}
	for _, args := range [][]string{nil, {"--app", c.app}, {"--app", c.app, "--deployment", "invalid", "--tenant", c.tenant}, append(append([]string{}, good...), "--self"), append(append([]string{}, good...), "--timeout", "-1s"), append(append([]string{}, good...), "extra")} {
		if _, err := parseCustomerOperationDoctor(args); err == nil {
			t.Fatal("invalid selectors accepted", args)
		}
	}
	if _, err := parseCustomerOperationDoctor(append(good, "--name", "export", "--timeout", "1s")); err != nil {
		t.Fatal(err)
	}
	cases := []func(*api.OperationDoctorResponse){func(r *api.OperationDoctorResponse) { r.Checks = r.Checks[:3] }, func(r *api.OperationDoctorResponse) { r.Checks = append(r.Checks, r.Checks[0]) }, func(r *api.OperationDoctorResponse) { r.SubmissionState = "ready" }, func(r *api.OperationDoctorResponse) { r.PlatformTenantID = developerDefinitionID }, func(r *api.OperationDoctorResponse) { r.ObservationScope = "fleet" }, func(r *api.OperationDoctorResponse) { r.ObservedAt = time.Time{} }, func(r *api.OperationDoctorResponse) { r.Checks = nil }, func(r *api.OperationDoctorResponse) { r.Checks[0].Status = "unrecognized" }, func(r *api.OperationDoctorResponse) { r.Checks[1].Status = "blocked" }, func(r *api.OperationDoctorResponse) { r.Checks[0].Status = "unknown"; r.SubmissionState = "eligible" }, func(r *api.OperationDoctorResponse) { r.Checks[0].Name = "another-operation" }}
	for i, mutate := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			changed := r
			changed.Checks = append([]api.OperationDoctorCheck{}, r.Checks...)
			mutate(&changed)
			f := &customerOperationDoctorFixture{report: changed}
			var out bytes.Buffer
			_, err := runCustomerOperationDoctor(t.Context(), f, c, &out, true)
			if err == nil || out.Len() != 0 {
				t.Fatal("invalid diagnostic report emitted", err, out.String())
			}
		})
	}
	f := &customerOperationDoctorFixture{err: context.DeadlineExceeded}
	_, err := runCustomerOperationDoctor(t.Context(), f, c, &bytes.Buffer{}, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("request deadline lost", err)
	}
}
