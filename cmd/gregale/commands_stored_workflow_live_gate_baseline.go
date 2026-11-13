// adr: 1302
package main

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

var errStoredWorkflowGateBaselinePolicyMismatch = errors.New("named baseline policy differs from local policy")
var errStoredWorkflowGateBaselineRetired = errors.New("named baseline retired")
var errStoredWorkflowGateBaselineInvalid = errors.New("invalid baseline receipt")

func resolveStoredWorkflowGateBaseline(ctx context.Context, client *api.Client, o *storedWorkflowLiveGateOptions) error {
	receipt, err := client.GetStoredWorkflowLiveBaseline(ctx, o.app, o.Name, o.baselineName)
	if err != nil {
		var failure *api.APIError
		if errors.As(err, &failure) && failure.Problem.Status == http.StatusGone {
			return errStoredWorkflowGateBaselineRetired
		}
		return err
	}
	policy, err := validateStoredWorkflowLiveBaselineReceipt(receipt, o.Name, o.baselineName)
	if err != nil {
		return err
	}
	o.BaselineID = uuid.MustParse(receipt.Suite.ID).String()
	o.resolvedBaseline = &receipt
	if o.Coverage != nil && o.Coverage.Hash() != policy.Hash() {
		return errStoredWorkflowGateBaselinePolicyMismatch
	}
	o.Coverage = &policy
	return nil
}

func validateStoredWorkflowLiveBaselineReceipt(receipt api.StoredWorkflowLiveBaseline, name, alias string) (storedworkflowgate.CoveragePolicy, error) {
	policy, err := storedworkflowgate.ParseCoveragePolicy(receipt.Policy)
	if err != nil || policy.Version != 2 || policy.Hash() != receipt.PolicyHash || receipt.Name != name || receipt.Suite.Name != name || receipt.BaselineName != alias || receipt.Revision <= 0 || receipt.PromotedAt.IsZero() || receipt.OldestRunCreatedAt.IsZero() || receipt.OldestRunCreatedAt.After(receipt.PromotedAt) || (receipt.Revision == 1 && receipt.PreviousPromotionID != "") || (receipt.Revision > 1 && receipt.PreviousPromotionID == "") || receipt.CasesPassed != receipt.Suite.CaseCount || receipt.CasesPassed < 1 || receipt.CasesPassed > api.StoredWorkflowLiveSuiteMaxCases || receipt.Suite.LinkedRuns != receipt.CasesPassed || receipt.EvidenceAgeSeconds < 0 || receipt.EvidenceAgeSeconds > float64(*policy.MaxCandidateAgeSeconds) {
		return policy, errStoredWorkflowGateBaselineInvalid
	}
	for _, value := range []string{receipt.PromotionID, receipt.AppID, receipt.ActorAccountID, receipt.Suite.ID, receipt.Suite.DeploymentID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return policy, errStoredWorkflowGateBaselineInvalid
		}
	}
	if receipt.PreviousPromotionID != "" {
		id, err := uuid.Parse(receipt.PreviousPromotionID)
		if err != nil || id == uuid.Nil {
			return policy, errStoredWorkflowGateBaselineInvalid
		}
	}
	if receipt.ActorAPIKeyID != "" {
		id, err := uuid.Parse(receipt.ActorAPIKeyID)
		if err != nil || id == uuid.Nil {
			return policy, errStoredWorkflowGateBaselineInvalid
		}
	}
	if receipt.Suite.Version <= 0 || receipt.Suite.PublishedVersion <= 0 || receipt.Suite.PublishedVersion > receipt.Suite.Version || receipt.Suite.CreatedAt.IsZero() || receipt.Suite.UpdatedAt.Before(receipt.Suite.CreatedAt) {
		return policy, errStoredWorkflowGateBaselineInvalid
	}
	return policy, nil
}
