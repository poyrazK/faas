// adr: 1302
package state

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storedworkflowgate"
)

type StoredWorkflowLiveBaselineStore interface {
	GetStoredWorkflowLiveBaseline(context.Context, string, string, string) (api.StoredWorkflowLiveBaseline, error)
	GetStoredWorkflowLiveBaselineHistory(context.Context, string, string, string) (api.StoredWorkflowLiveBaselineHistory, error)
	PromoteStoredWorkflowLiveBaseline(context.Context, string, string, string, string, string, string, api.PromoteStoredWorkflowLiveBaselineRequest) (api.StoredWorkflowLiveBaseline, error)
}

var ErrStoredWorkflowLiveBaselineConflict = errors.New("baseline revision or promotion identity changed")

func validateBaselinePromotion(alias, promotionID, actor, key string, request *api.PromoteStoredWorkflowLiveBaselineRequest) (storedworkflowgate.CoveragePolicy, error) {
	invalid := api.ErrValidation("Provide a baseline name, promotion UUID, expected revision and target, suite UUID, and version 2 coverage/age policy.")
	p, err := storedworkflowgate.ParseCoveragePolicy(request.Policy)
	if err != nil || p.Version != 2 || !api.ValidStoredWorkflowLiveBaselineName(alias) || request.ExpectedRevision < 0 || request.ExpectedRevision == math.MaxInt64 {
		return p, invalid
	}
	for _, value := range []string{promotionID, actor, request.SuiteID, request.ExpectedDeploymentID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return p, invalid
		}
	}
	if key != "" {
		id, err := uuid.Parse(key)
		if err != nil || id == uuid.Nil {
			return p, invalid
		}
	}
	s := storedworkflowgate.Selection{Name: "promotion", CandidateID: request.SuiteID, Expected: storedworkflowgate.Target{Version: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: request.ExpectedDeploymentID}, Coverage: &p}
	if s.ValidateCandidate() != nil {
		return p, invalid
	}
	request.SuiteID = uuid.MustParse(request.SuiteID).String()
	request.ExpectedDeploymentID = uuid.MustParse(request.ExpectedDeploymentID).String()
	request.Policy, _ = json.Marshal(p)
	if int64(len(request.Policy)) > api.StoredWorkflowLiveGatePolicyMaxBytes {
		return p, api.ErrRequestBodyTooLarge(api.StoredWorkflowLiveGatePolicyMaxBytes, int64(len(request.Policy)))
	}
	return p, nil
}

func baselineReplay(receipt api.StoredWorkflowLiveBaseline, appID, name, alias, actor, key string, request api.PromoteStoredWorkflowLiveBaselineRequest, policy storedworkflowgate.CoveragePolicy) bool {
	return receipt.AppID == appID && receipt.Name == name && receipt.BaselineName == alias && receipt.ActorAccountID == actor && receipt.ActorAPIKeyID == key && receipt.Revision == request.ExpectedRevision+1 && receipt.Suite.ID == request.SuiteID && receipt.Suite.Version == request.ExpectedVersion && receipt.Suite.PublishedVersion == request.ExpectedPublishedVersion && receipt.Suite.DeploymentID == request.ExpectedDeploymentID && receipt.PolicyHash == policy.Hash()
}

func promotedBaseline(appID, name, alias, promotionID, actor, key string, request api.PromoteStoredWorkflowLiveBaselineRequest, policy storedworkflowgate.CoveragePolicy, prior api.StoredWorkflowLiveBaseline, view api.StoredWorkflowLiveSuite) (api.StoredWorkflowLiveBaseline, error) {
	now := time.Now().UTC()
	selection := storedworkflowgate.Selection{Name: name, BaselineID: view.ID, CandidateID: view.ID, Expected: storedworkflowgate.Target{Version: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: request.ExpectedDeploymentID}, Coverage: &policy}
	result, err := storedworkflowgate.EvaluatePromotionAt(selection, compareLiveSuites(view, view), now)
	if err != nil {
		return api.StoredWorkflowLiveBaseline{}, err
	}
	if result.ExitCode != storedworkflowgate.ExitPassed || result.EvidenceAge == nil {
		return api.StoredWorkflowLiveBaseline{}, api.NewProblem(409, api.CodeConflict, "Suite cannot be promoted", "The suite must pass the explicit current target, required coverage and evidence-age policy. No baseline was changed.")
	}
	receipt := api.StoredWorkflowLiveBaseline{PromotionID: promotionID, AppID: appID, Name: name, BaselineName: alias, Revision: request.ExpectedRevision + 1, PreviousPromotionID: prior.PromotionID, ActorAccountID: actor, ActorAPIKeyID: key, PromotedAt: now, Suite: view.StoredWorkflowLiveSuiteSummary, Policy: append(json.RawMessage(nil), request.Policy...), PolicyHash: policy.Hash(), CasesPassed: result.CandidatePassed, OldestRunCreatedAt: result.EvidenceAge.OldestRunCreatedAt, EvidenceAgeSeconds: result.EvidenceAge.AgeSeconds}
	return receipt, nil
}

func cloneBaseline(receipt api.StoredWorkflowLiveBaseline) api.StoredWorkflowLiveBaseline {
	receipt.Policy = append(json.RawMessage(nil), receipt.Policy...)
	return receipt
}

func baselineLimitError(observed int64) error {
	return api.NewProblem(409, api.CodeConflict, "Named baseline limit reached", "Reuse an active baseline name or explicitly retire an unused name for this stored_workflow.").WithLimit(api.StoredWorkflowLiveBaselineLimit, observed).WithDocs("https://gregale.dev/docs/workflows/stored#baselines")
}
