package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getOperationDoctor(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), api.OperationDoctorMaxDuration)
	defer cancel()
	r = r.WithContext(ctx)
	dep, tenant, definitions, ok := s.loadOperationDoctorInput(w, r, acct)
	if !ok {
		return
	}
	report := s.observeOperationDoctor(ctx, acct, dep, tenant, definitions)
	if ctx.Err() != nil {
		api.WriteProblem(w, api.ErrCapacity("Operations observation timed out; retry diagnostics"))
		return
	}
	if len(report.Checks) > api.OperationDoctorChecksMax {
		api.WriteProblem(w, api.ErrCapacity("Operations diagnostic projection exceeds its bound"))
		return
	}
	report.SubmissionState = report.ObservedSubmissionState()
	writeJSON(w, http.StatusOK, report)
}

func (s *server) loadOperationDoctorInput(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Deployment, state.PlatformTenant, []state.OperationDefinition, bool) {
	store, ok := s.operationStore(w)
	if !ok {
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	dep, ok := s.operationDefinitionDeployment(w, r, acct)
	if !ok {
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	tenantID, name, err := operationDoctorSelectors(r)
	if err != nil {
		writeOperationError(w, err)
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	tenants, ok := s.store.(state.PlatformTenantStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Operations tenant diagnostics are unavailable"))
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	tenant, err := tenants.GetPlatformTenant(r.Context(), acct.ID, tenantID)
	if err != nil {
		writeOperationError(w, err)
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	definitions, err := store.OperationDefinitionsForDeployment(r.Context(), acct.ID, dep.AppID, dep.ID)
	if err != nil {
		writeOperationError(w, err)
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	definitions, err = operationDoctorDefinitions(definitions, name)
	if err != nil {
		writeOperationError(w, err)
		return state.Deployment{}, state.PlatformTenant{}, nil, false
	}
	return dep, tenant, definitions, true
}

func operationDoctorSelectors(r *http.Request) (string, string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err == nil {
		for key, values := range q {
			if (key != "tenant_id" && key != "name") || len(values) != 1 || values[0] == "" {
				err = state.ErrInvalidArgument
				break
			}
		}
	}
	if err != nil {
		return "", "", state.ErrInvalidArgument
	}
	id, err := uuid.Parse(q.Get("tenant_id"))
	if err != nil || id == uuid.Nil || len(q.Get("name")) > api.OperationNameMaxBytes || (q.Has("name") && q.Get("name") == "") {
		return "", "", state.ErrInvalidArgument
	}
	return id.String(), q.Get("name"), nil
}

func operationDoctorDefinitions(definitions []state.OperationDefinition, name string) ([]state.OperationDefinition, error) {
	if len(definitions) > api.MustLimitsFor(api.PlanScale).Operations.DefinitionsPerApp {
		return nil, state.ErrInvalidArgument
	}
	if name != "" {
		for _, d := range definitions {
			if d.Spec.Name == name {
				return []state.OperationDefinition{d}, nil
			}
		}
		return nil, state.ErrNotFound
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Spec.Name < definitions[j].Spec.Name })
	return definitions, nil
}

func (s *server) observeOperationDoctor(ctx context.Context, acct state.Account, dep state.Deployment, tenant state.PlatformTenant, definitions []state.OperationDefinition) api.OperationDoctorResponse {
	r := api.OperationDoctorResponse{AppID: dep.AppID, Scope: dep.Scope, DeploymentID: dep.ID, PlatformTenantID: tenant.ID, Plan: acct.Plan, ObservedAt: time.Now().UTC(), ObservationScope: "responding_api_node", Checks: []api.OperationDoctorCheck{}}
	limits := api.MustLimitsFor(acct.Plan).Operations
	r.Checks = append(r.Checks, operationDoctorFact("tenant", tenant.Status == state.PlatformTenantActive, "tenant_active_observed", "tenant_suspended", "Tenant ownership and active status observed.", "Resume this tenant through the owning account before new work."))
	r.Checks = append(r.Checks, operationDoctorFact("plan", limits.Allowed, "plan_allowed_observed", "plan_not_allowed", "Current plan permits Operations.", "Select a plan with an Operations allowance."))
	r.Checks = append(r.Checks, operationDoctorFact("deployment_pin", dep.Status == state.DeployLive, "deployment_live_observed", "deployment_not_live", "Selected deployment metadata is live; code bytes were not probed.", "Select a live deployment and its immutable definitions."))
	r.Checks = append(r.Checks, s.observeOperationPending(ctx, acct.ID, int64(limits.PendingPerAccount)))
	p := s.operationsPreview.ObserveCohort(acct.ID, dep.AppID, dep.Scope, tenant.ID)
	preview := operationDoctorFact("preview", p.Allowed, p.Code, p.Code, "Selected tenant is in the currently observed preview window.", "Have the platform operator inspect the bounded cohort and UTC window; this report grants no admission.")
	r.Checks = append(r.Checks, preview)
	r.Checks = append(r.Checks, operationDoctorConfiguration("workload_trust", s.operationsWorkloadVerifier != nil, "Configure valid Operations workload trust on the API node."), operationDoctorConfiguration("result_storage", s.operationArtifactStorage != nil, "Configure private retained-result storage on the API node."))
	if len(definitions) == 0 {
		r.Checks = append(r.Checks, operationDoctorFact("definitions", false, "definitions_observed", "definitions_missing", "No immutable Operations definitions were found.", "Deploy source-local Operations declarations through the existing admission policy."))
	}
	for _, d := range definitions {
		if ctx.Err() != nil {
			break
		}
		r.Checks = append(r.Checks, s.observeOperationDefinition(ctx, acct, dep, d)...)
		r.Checks = append(r.Checks, operationDoctorExecutionPreview(p, d))
	}
	for _, name := range []string{"gateway_admission", "runtime_reporting", "result_storage_io", "native_lifecycle", "fleet_rollback"} {
		r.Checks = append(r.Checks, api.OperationDoctorCheck{Check: name, Status: "unknown", Impact: "qualification", Code: name + "_unverified", Message: "Not probed by this read-only API observation.", Remediation: "Use exact-source native and fleet qualification receipts; node configuration is not runtime proof."})
	}
	return r
}

func operationDoctorFact(name string, passed bool, good, bad, message, remediation string) api.OperationDoctorCheck {
	c := api.OperationDoctorCheck{Check: name, Impact: "submission", Status: "observed", Code: good, Message: message}
	if !passed {
		c.Status, c.Code, c.Message, c.Remediation = "blocked", bad, operationDoctorBlockerMessage(bad), remediation
	}
	return c
}

func operationDoctorConfiguration(name string, configured bool, remediation string) api.OperationDoctorCheck {
	c := operationDoctorFact(name, configured, name+"_configured", name+"_missing", "API node configuration is present; runtime usability is unverified.", remediation)
	if configured {
		c.Status = "configured"
	}
	return c
}

func (s *server) observeOperationPending(ctx context.Context, account string, limit int64) api.OperationDoctorCheck {
	c := api.OperationDoctorCheck{Check: "pending_capacity", Impact: "submission", Status: "unknown", Code: "pending_capacity_unavailable", Message: "Account-wide pending capacity could not be observed.", Remediation: "Restore state reads and rerun diagnostics; capacity has not been reserved.", Limit: &limit}
	store, ok := s.store.(state.OperationDiagnosticStore)
	if !ok {
		return c
	}
	count, err := store.OperationPendingCount(ctx, account)
	if err != nil || count < 0 {
		return c
	}
	c = operationDoctorFact("pending_capacity", count < limit, "pending_capacity_observed", "pending_capacity_exhausted", "Account-wide pending count observed; includes work requiring reconciliation and reserves no slot.", "Inspect pending and reconciliation work; do not blindly repeat uncertain effects.")
	c.Limit, c.Observed = &limit, &count
	return c
}

func operationDoctorReadFailure(err error) bool {
	return errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrInvalidArgument)
}

func operationDoctorBlockerMessage(code string) string {
	switch code {
	case "tenant_suspended":
		return "The selected tenant is suspended."
	case "plan_not_allowed":
		return "The current account plan has no Operations allowance."
	case "deployment_not_live":
		return "The selected deployment metadata is not live."
	case "pending_capacity_exhausted":
		return "The account-wide pending Operations limit is exhausted."
	case "preview_not_configured":
		return "No preview policy is configured on this API node."
	case "preview_policy_unavailable":
		return "The current preview policy cannot be safely read or validated; admission fails closed."
	case "preview_disabled":
		return "The current preview policy disables new admission."
	case "preview_not_started":
		return "The preview admission window has not started."
	case "preview_expired":
		return "The preview admission window has expired."
	case "preview_cohort_excluded":
		return "The selected account, app, environment and tenant are outside the observed cohort."
	case "preview_execution_kind_excluded":
		return "This definition's execution type is outside the selected cohort's allowlist."
	case "preview_execution_kind_invalid":
		return "The pinned definition has an ambiguous execution type."
	case "workload_trust_missing":
		return "Operations workload trust is not configured on this API node."
	case "result_storage_missing":
		return "Private retained-result storage is not configured on this API node."
	case "definitions_missing":
		return "The selected deployment has no immutable Operations definitions."
	case "definition_contract_unusable":
		return "The pinned contract, revision or deployment binding is unusable under the current plan."
	case "release_pin_unavailable":
		return "The pinned release is expired, unavailable or does not resolve to this live deployment."
	default:
		return "Submission prerequisite was not satisfied at observation time."
	}
}
