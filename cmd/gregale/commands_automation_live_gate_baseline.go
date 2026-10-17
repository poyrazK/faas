// adr: 1302
package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/automationgate"
)

var errAutomationGateBaselinePolicyMismatch = errors.New("named baseline policy differs from local policy")
var errAutomationGateBaselineRetired = errors.New("named baseline retired")
var errAutomationGateBaselineInvalid = errors.New("invalid baseline receipt")

func resolveAutomationGateBaseline(ctx context.Context, client *api.Client, o *automationLiveGateOptions) error {
	receipt, err := client.GetAutomationLiveBaseline(ctx, o.app, o.Name, o.baselineName)
	if err != nil {
		var failure *api.APIError
		if errors.As(err, &failure) && failure.Problem.Status == http.StatusGone {
			return errAutomationGateBaselineRetired
		}
		return err
	}
	policy, err := validateAutomationLiveBaselineReceipt(receipt, o.Name, o.baselineName)
	if err != nil {
		return err
	}
	o.BaselineID = uuid.MustParse(receipt.Suite.ID).String()
	o.resolvedBaseline = &receipt
	if o.Coverage != nil && o.Coverage.Hash() != policy.Hash() {
		return errAutomationGateBaselinePolicyMismatch
	}
	o.Coverage = &policy
	return nil
}

func validateAutomationLiveBaselineReceipt(receipt api.AutomationLiveBaseline, name, alias string) (automationgate.CoveragePolicy, error) {
	policy, err := automationgate.ParseCoveragePolicy(receipt.Policy)
	if err != nil || policy.Version != 2 || policy.Hash() != receipt.PolicyHash || receipt.Name != name || receipt.Suite.Name != name || receipt.BaselineName != alias || receipt.Revision <= 0 || receipt.PromotedAt.IsZero() || receipt.OldestRunCreatedAt.IsZero() || receipt.OldestRunCreatedAt.After(receipt.PromotedAt) || (receipt.Revision == 1 && receipt.PreviousPromotionID != "") || (receipt.Revision > 1 && receipt.PreviousPromotionID == "") || receipt.CasesPassed != receipt.Suite.CaseCount || receipt.CasesPassed < 1 || receipt.CasesPassed > api.AutomationLiveSuiteMaxCases || receipt.Suite.LinkedRuns != receipt.CasesPassed || receipt.EvidenceAgeSeconds < 0 || receipt.EvidenceAgeSeconds > float64(*policy.MaxCandidateAgeSeconds) {
		return policy, errAutomationGateBaselineInvalid
	}
	for _, value := range []string{receipt.PromotionID, receipt.AppID, receipt.ActorAccountID, receipt.Suite.ID, receipt.Suite.DeploymentID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return policy, errAutomationGateBaselineInvalid
		}
	}
	if receipt.PreviousPromotionID != "" {
		id, err := uuid.Parse(receipt.PreviousPromotionID)
		if err != nil || id == uuid.Nil {
			return policy, errAutomationGateBaselineInvalid
		}
	}
	if receipt.ActorAPIKeyID != "" {
		id, err := uuid.Parse(receipt.ActorAPIKeyID)
		if err != nil || id == uuid.Nil {
			return policy, errAutomationGateBaselineInvalid
		}
	}
	if receipt.Suite.Version <= 0 || receipt.Suite.PublishedVersion <= 0 || receipt.Suite.PublishedVersion > receipt.Suite.Version || receipt.Suite.CreatedAt.IsZero() || receipt.Suite.UpdatedAt.Before(receipt.Suite.CreatedAt) {
		return policy, errAutomationGateBaselineInvalid
	}
	return policy, nil
}
