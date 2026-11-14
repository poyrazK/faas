package mcpmanagement

import (
	"context"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func validatePublicationRecord(record api.ToolPublicationRecord, account, id string, intent *api.ToolPublicationIntent) error {
	if account == "" || record.ID != id || record.AccountID != account || !toolpublication.ValidID(id) || toolpublication.ValidateIntent(record.Intent) != nil ||
		(intent != nil && !reflect.DeepEqual(record.Intent, *intent)) || record.CreatedAt.IsZero() || !record.Deadline.After(record.CreatedAt) || record.EvidenceOrigin != "publisher_attested" {
		return invalid("invalid_response", "native publication record differs from its reviewed identity")
	}
	switch record.Status {
	case "running", "completed", "failed", "outcome_unknown":
	default:
		return invalid("invalid_response", "unknown native publication status")
	}
	if record.Status == "running" && record.Outcome != nil || record.Outcome != nil && record.Outcome.Status != record.Status {
		return invalid("invalid_response", "native publication state contradicts its outcome")
	}
	if record.Status == "completed" && (record.Outcome == nil || record.Outcome.Status != "completed" || toolpublication.ValidateOutcome(record.Intent, *record.Outcome) != nil || !toolpublication.ValidID(record.DeploymentSourceSHA256)) {
		return invalid("invalid_response", "completed publication lacks bound catalog and test evidence")
	}
	return nil
}

func (m *management) getDurableToolDeploy(ctx context.Context, id string) (any, error) {
	if !toolpublication.ValidID(id) {
		return nil, invalid("invalid_argument", "plan_id must be a publication identity")
	}
	account, err := m.client.Whoami(ctx)
	if err != nil {
		return nil, err
	}
	record, err := m.client.GetToolPublication(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = validatePublicationRecord(record, account.ID, id, nil); err != nil {
		return nil, err
	}
	return durablePublicationView(record), nil
}

func durablePublicationView(record api.ToolPublicationRecord) map[string]any {
	record = toolpublication.View(record, time.Now())
	var outcome *api.ToolPublicationOutcome
	if record.Outcome != nil {
		copy := *record.Outcome
		copy.Catalog = nil
		outcome = &copy
	}
	recovery := Recovery{Action: "inspect", Tool: "gregale_get_tool_deploy", Arguments: map[string]any{"plan_id": record.ID}}
	if record.Status == "running" {
		delay := int64(2)
		recovery.AfterSeconds = &delay
	}
	if record.Status == "completed" {
		recovery = Recovery{Action: "none"}
	}
	if record.Status == "outcome_unknown" || record.Status == "failed" {
		recovery = Recovery{Action: "inspect", Toolsets: []string{ToolsetCore}}
	}
	return map[string]any{"schema_version": 1, "plan_id": record.ID, "action": "tool_deploy", "account_id": record.AccountID,
		"app": record.Intent.App, "app_id": record.Intent.AppID, "source_sha256": record.Intent.SourceSHA256,
		"status": record.Status, "durable": true, "deadline": record.Deadline, "recovery": recovery,
		"deployment_source_sha256": record.DeploymentSourceSHA256, "result": outcome, "evidence_origin": record.EvidenceOrigin}
}

func (m *management) persistToolPublication(ctx context.Context, p *toolPublication, status string, raw publicationCLIReceipt) error {
	out := api.ToolPublicationOutcome{Status: status}
	if status == "completed" {
		catalog, err := mcphosting.NewCatalogContract(mcphosting.ProtocolVersion, mcphosting.Catalog{Tools: raw.Verification.Tools, Capabilities: raw.Verification.Capabilities,
			Extensions: raw.Verification.Extensions, Resources: raw.Verification.Resources, ResourceTemplates: raw.Verification.ResourceTemplates, Prompts: raw.Verification.Prompts})
		if err != nil {
			return invalid("invalid_response", "publication receipt has no valid observed runtime catalog")
		}
		out.Catalog, err = mcphosting.MarshalContract(catalog)
		if err != nil {
			return err
		}
		out.DeploymentID, out.CatalogSHA256, out.ProtocolVerified = raw.DeploymentID, raw.CatalogIntegrity.Pin.SHA256, true
		for _, check := range raw.SmokeTests.Checks {
			out.Checks = append(out.Checks, api.ToolPublicationCheck{Name: check.Name, Status: check.Status})
		}
	}
	if err := toolpublication.ValidateOutcome(p.Intent, out); err != nil {
		return invalid("invalid_response", "publication catalog or evidence differs from the review")
	}
	// Saving evidence performs no customer work. A bounded final save is useful
	// even when the worker deadline elapsed. The API still authenticates it.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	record, err := m.client.FinishToolPublication(saveCtx, p.ID, out)
	if err != nil {
		return err
	}
	if err = validatePublicationRecord(record, p.AccountID, p.ID, &p.Intent); err != nil {
		return err
	}
	if record.Status != status || record.Outcome == nil || !reflect.DeepEqual(*record.Outcome, out) {
		return invalid("invalid_response", "native publication outcome differs from the publisher receipt")
	}
	return nil
}
