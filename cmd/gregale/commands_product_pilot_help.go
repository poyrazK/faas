package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/productcontrol"
)

func verifyProductPilotHelp(ctx context.Context, client productPilotClient, m api.ProductPilotManifest, r *api.ProductPilotReport) error {
	if len(m.HelpCases) > 0 {
		r.HelpState = "observed"
	}
	for _, item := range m.HelpCases {
		value, err := client.GetProductOwnerHelp(ctx, m.App, item.RequestID)
		if err != nil {
			return fmt.Errorf("read help case %s: %w", item.Name, err)
		}
		if err := validateProductPilotHelpResponse(m, item, value); err != nil {
			return err
		}
		summary, err := productPilotHelpSummary(item.Name, value)
		if err != nil {
			return err
		}
		matched := value.Request.Kind == item.Kind && value.Request.Feature == item.Feature && value.Request.OperationID == item.OperationID && value.Status == item.ExpectedStatus && value.Version == item.ExpectedVersion && summary.MessageSHA256 == item.MessageSHA256 && summary.ResponseSHA256 == item.ResponseSHA256 && summary.ContextSHA256 == item.ContextSHA256 && pilotHelpRunTermsMatch(value, r)
		status, code := "observed", "help_snapshot_and_response_match"
		if !matched {
			status, code, r.HelpState = "blocked", "help_expectation_mismatch", "blocked"
		}
		summary.Result = status
		r.Help = append(r.Help, summary)
		r.Checks = append(r.Checks, api.ProductDoctorCheck{Check: item.Name, Status: status, Impact: "help_evidence", Code: code, Message: "Compared scoped retained request, original text/context hashes and current owner status/response; no customer text is included. Customer-session visibility and recovery require live browser evidence."})
	}
	return nil
}

func inspectProductPilotHelp(ctx context.Context, client productPilotClient, c productCommand) (api.ProductPilotHelpResult, error) {
	doctor, err := client.GetProductDoctor(ctx, c.app, "")
	if err != nil {
		return api.ProductPilotHelpResult{}, err
	}
	if err := validateProductDoctorReport(doctor, ""); err != nil {
		return api.ProductPilotHelpResult{}, err
	}
	value, err := client.GetProductOwnerHelp(ctx, c.app, c.helpID)
	if err != nil {
		return api.ProductPilotHelpResult{}, err
	}
	if err := validateProductPilotHelpResponse(api.ProductPilotManifest{AppID: doctor.AppID}, api.ProductPilotHelpCase{Name: "help-inspection", TenantID: c.tenant, RequestID: c.helpID}, value); err != nil {
		return api.ProductPilotHelpResult{}, err
	}
	return productPilotHelpSummary("help-inspection", value)
}

func productPilotHelpSummary(name string, value api.ProductHelpRequest) (api.ProductPilotHelpResult, error) {
	message, response, captured, err := productcontrol.PilotHelpHashes(value)
	if err != nil {
		return api.ProductPilotHelpResult{}, err
	}
	return api.ProductPilotHelpResult{Name: name, TenantID: value.TenantID, RequestID: value.Request.ID, Kind: value.Request.Kind, Status: value.Status, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, MessageSHA256: message, ResponseSHA256: response, ContextSHA256: captured, Result: "inspection_only"}, nil
}

func validateProductPilotHelpResponse(m api.ProductPilotManifest, c api.ProductPilotHelpCase, value api.ProductHelpRequest) error {
	wanted, wantErr := uuid.Parse(m.AppID)
	actual, actualErr := uuid.Parse(value.AppID)
	if wantErr != nil || actualErr != nil || wanted == uuid.Nil || actual != wanted || value.TenantID != c.TenantID || value.Request.ID != c.RequestID {
		return fmt.Errorf("help case %s returned evidence for another app/customer/request", c.Name)
	}
	if productcontrol.ValidateHelpCreate(value.Request) != nil || !productcontrol.HelpStatus(value.Status) || value.Version < 1 || value.Version > api.ProductControlHelpMaxUpdates || value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) || value.Context.ObservedAt.IsZero() || (value.Response != "" && !productcontrol.HelpText(value.Response)) {
		return fmt.Errorf("help case %s returned malformed request evidence", c.Name)
	}
	if (value.Version == 1 && (value.Status != "open" || value.Response != "" || !value.UpdatedAt.Equal(value.CreatedAt))) || (value.Version > 1 && value.Response == "") {
		return fmt.Errorf("help case %s returned inconsistent response/version evidence", c.Name)
	}
	return nil
}

func pilotHelpRunTermsMatch(value api.ProductHelpRequest, r *api.ProductPilotReport) bool {
	if value.Request.Kind != "run" {
		return true
	}
	for _, op := range r.Operations {
		if op.Evidence.OperationID == value.Request.OperationID {
			terms := op.Evidence.Receipt
			return value.Context.Feature == terms.Feature && value.Context.Revision == terms.Revision && value.Context.Plan == terms.Plan && value.Context.Period == terms.Period
		}
	}
	return false
}
