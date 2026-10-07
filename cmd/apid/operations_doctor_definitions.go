package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) observeOperationDefinition(ctx context.Context, acct state.Account, dep state.Deployment, d state.OperationDefinition) []api.OperationDoctorCheck {
	contract, err := operations.Compile(d.Spec, api.MustLimitsFor(acct.Plan).Operations)
	valid := err == nil && contract.Revision == d.Revision && d.AccountID == acct.ID && d.AppID == dep.AppID && d.DeploymentID == dep.ID && d.Scope == dep.Scope
	checks := []api.OperationDoctorCheck{operationDoctorFact("definition_contract", valid, "definition_revision_observed", "definition_contract_unusable", "Immutable contract revision and deployment binding match the current plan.", "Inspect the pinned definition and current plan; deploy a corrected contract without editing existing work.")}
	pin := api.OperationDoctorCheck{Check: "release_pin", Status: "not_requested", Impact: "submission", Code: "release_pin_not_requested", Message: "Definition pins a deployment without an explicit release set."}
	if d.ReleaseID != "" {
		var selected string
		var err error
		releases, ok := s.store.(state.ProjectReleaseSetStore)
		if ok {
			_, selected, err = releases.ResolveProjectRelease(ctx, dep.AppID, dep.Scope, d.ReleaseID)
		} else {
			err = fmt.Errorf("release observation is unsupported")
		}
		pin = operationDoctorFact("release_pin", err == nil && selected == dep.ID, "release_pin_observed", "release_pin_unavailable", "Pinned public release resolves to the selected live deployment.", "Inspect release expiry, membership and deployment availability; do not repin accepted work.")
		if err != nil && !operationDoctorReadFailure(err) {
			pin.Status, pin.Code, pin.Message, pin.Remediation = "unknown", "release_pin_unverified", "Release metadata could not be observed.", "Restore release metadata reads and rerun diagnostics."
		}
	}
	checks = append(checks, pin, s.observeOperationCompletion(ctx, acct.ID, dep.AppID, d.Spec.CompletionWebhookID))
	for i := range checks {
		checks[i].DefinitionID, checks[i].Name, checks[i].Revision, checks[i].ReleaseID = d.ID, d.Spec.Name, d.Revision, d.ReleaseID
	}
	return checks
}

func (s *server) observeOperationCompletion(ctx context.Context, account, app, webhook string) api.OperationDoctorCheck {
	c := api.OperationDoctorCheck{Check: "completion_destination", Status: "not_requested", Impact: "delivery", Code: "completion_not_requested", Message: "No completion webhook is declared."}
	if webhook == "" {
		return c
	}
	c.Status, c.Code, c.Message, c.Remediation = "warning", "completion_destination_unavailable", "Completion destination is unavailable for this app.", "Inspect the owning app webhook; delivery faults do not authorize repeating business work."
	hook, err := s.store.AppWebhookByID(ctx, webhook)
	if err != nil {
		if !operationDoctorReadFailure(err) {
			c.Status, c.Code, c.Message = "unknown", "completion_destination_unverified", "Completion destination metadata could not be observed."
		}
		return c
	}
	if hook.AccountID != account || hook.AppID != app {
		return c
	}
	if !hook.Enabled {
		c.Code, c.Message = "completion_destination_disabled", "Completion webhook is disabled."
		return c
	}
	if len(hook.EventFilter) > 0 && !slices.Contains(hook.EventFilter, string(state.AppWebhookEventOperationFinished)) {
		c.Code, c.Message = "completion_event_excluded", "Completion webhook does not accept operation.finished."
		return c
	}
	c.Status, c.Code, c.Message, c.Remediation = "configured", "completion_destination_configured", "Owned webhook is enabled and accepts completion events; destination transport was not probed.", "Observe real delivery receipts to verify transport; never regenerate successful work to repair notifications."
	return c
}
