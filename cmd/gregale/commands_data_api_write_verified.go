package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var errDataAPIInspectionUnsupported = errors.New("server does not support verified Data API inspection")

type dataAPIWriteVerifiedClient interface {
	dataAPIWriteHostedClient
	CreateDataAPIInspection(context.Context, string, api.CreateDataAPIInspectionRequest) (api.DataAPIInspectionResponse, error)
	GetDataAPIInspection(context.Context, string, string) (api.DataAPIInspectionResponse, error)
}

func inspectDataAPIWriteVerifiedHosted(ctx context.Context, client dataAPIWriteVerifiedClient, appSlug, databaseID, bindingID string, scope dataAPIWriteQualification) (dataAPIWriteHostedReport, error) {
	var empty dataAPIWriteHostedReport
	validated, err := dataAPIWriteScope(scope.Schema, scope.Table, strings.Join(scope.Operations, ","))
	if err != nil || scope.Schema != "api" || !slices.Equal(validated.Operations, scope.Operations) || !api.ValidAppSlug(appSlug) || databaseID == "" || bindingID == "" {
		return empty, errors.New("invalid inspection scope")
	}
	before, err := observeDataAPIWriteBinding(ctx, client, appSlug, databaseID, bindingID)
	if err != nil {
		var selection dataAPIWriteInspectionError
		if errors.As(err, &selection) {
			return empty, selection
		}
		return empty, dataAPIWriteInspectionError{"binding_or_deployment_unavailable"}
	}
	task, err := client.CreateDataAPIInspection(ctx, appSlug, api.CreateDataAPIInspectionRequest{DatabaseID: databaseID, BindingID: bindingID, Table: scope.Table})
	if err != nil {
		var apiErr *api.APIError
		if errors.As(err, &apiErr) && apiErr.Problem.Status == http.StatusNotFound {
			return empty, errDataAPIInspectionUnsupported
		}
		return empty, dataAPIWriteInspectionError{"inspection_task_unavailable"}
	}
	if task.TaskID == "" {
		return empty, dataAPIWriteInspectionError{"inspection_task_unavailable"}
	}
	id := task.TaskID
	validSelection := func(value api.DataAPIInspectionResponse) bool {
		return value.TaskID == id && value.AppID == before.appID && value.DeploymentID == before.deploymentID && value.DatabaseID == databaseID && value.BindingID == bindingID && value.CredentialGeneration == before.generation
	}
	if !validSelection(task) {
		return empty, dataAPIWriteInspectionError{"inspection_task_changed"}
	}
	for !task.TaskStatus.Terminal() {
		if task.BindingIdentity.Status != "unknown" || task.BindingIdentity.Reason != "inspection_pending" {
			return empty, dataAPIWriteInspectionError{dataAPIInspectionUnknownReason(task.BindingIdentity.Reason)}
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return empty, dataAPIWriteInspectionError{"inspection_timed_out_or_cancelled"}
		case <-timer.C:
		}
		task, err = client.GetDataAPIInspection(ctx, appSlug, id)
		if err != nil {
			return empty, dataAPIWriteInspectionError{"inspection_task_unavailable"}
		}
		if !validSelection(task) {
			return empty, dataAPIWriteInspectionError{"inspection_task_changed"}
		}
	}
	if task.TaskStatus != api.AppTaskStatusSucceeded {
		return empty, dataAPIWriteInspectionError{"inspection_task_failed_or_incomplete"}
	}
	if task.BindingIdentity.Status != "verified" && task.BindingIdentity.Status != "mismatch" {
		return empty, dataAPIWriteInspectionError{dataAPIInspectionUnknownReason(task.BindingIdentity.Reason)}
	}
	if task.BindingIdentity.Status == "verified" && task.BindingIdentity.Reason != "provider_target_and_trust_matched" || task.BindingIdentity.Status == "mismatch" && task.BindingIdentity.Reason != "observed_target_differs" {
		return empty, dataAPIWriteInspectionError{"inspection_identity_unverified"}
	}
	// The server emits metadata only after validating authenticated execution
	// and the provider-pinned TLS trust. Its observation omits private digests.
	var fields map[string]json.RawMessage
	if json.Unmarshal(task.Observation, &fields) != nil || fields == nil {
		return empty, dataAPIWriteInspectionError{"inspection_output_incomplete_or_invalid"}
	}
	fields["transport"] = json.RawMessage(`{"encrypted":true,"peer_authorized":true}`)
	content, err := json.Marshal(fields)
	if err != nil {
		return empty, dataAPIWriteInspectionError{"inspection_output_incomplete_or_invalid"}
	}
	observation, err := parseDataAPIWriteObservation(string(content), scope.Table)
	if err != nil {
		return empty, dataAPIWriteInspectionError{"inspection_output_incomplete_or_invalid"}
	}
	after, err := observeDataAPIWriteBinding(ctx, client, appSlug, databaseID, bindingID)
	if err != nil || after != before {
		return empty, dataAPIWriteInspectionError{"binding_or_deployment_changed"}
	}
	return dataAPIWriteHostedReport{Version: 1, Status: "review_required", AppID: before.appID, DatabaseID: databaseID, BindingID: bindingID, CredentialGeneration: before.generation, DeploymentID: before.deploymentID, ConfigurationHash: before.configurationHash, TaskID: id, Scope: scope, Observation: observation, Findings: dataAPIWriteFindings(observation, scope), Authorization: "not_tested", BindingDelivery: "not_independently_verified", BindingIdentity: dataAPIWriteBindingIdentity{Status: task.BindingIdentity.Status, Reason: task.BindingIdentity.Reason}, Notes: []string{
		"The binding verdict applies to the observed database target, login and trusted TLS peer at inspection time.",
		"Policy expressions, membership data, function bodies, trigger effects, sequences and actual application identities are not evaluated.",
		"Catalog observations are point-in-time and cannot establish tenant isolation, deployed replica identity or future behavior.",
	}}, nil
}

func dataAPIInspectionUnknownReason(reason string) string {
	switch reason {
	case "binding_or_deployment_changed", "inspection_failed_or_stale", "execution_evidence_unavailable", "inspection_output_invalid", "provider_expectation_unavailable", "provider_expectation_changed", "inspection_trust_mismatch":
		return reason
	default:
		return "inspection_identity_unverified"
	}
}
