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
	"github.com/onebox-faas/faas/pkg/automationgate"
)

type AutomationLiveBaselineStore interface {
	GetAutomationLiveBaseline(context.Context, string, string, string) (api.AutomationLiveBaseline, error)
	GetAutomationLiveBaselineHistory(context.Context, string, string, string) (api.AutomationLiveBaselineHistory, error)
	PromoteAutomationLiveBaseline(context.Context, string, string, string, string, string, string, api.PromoteAutomationLiveBaselineRequest) (api.AutomationLiveBaseline, error)
}

var ErrAutomationLiveBaselineConflict = errors.New("baseline revision or promotion identity changed")

func validateBaselinePromotion(alias, promotionID, actor, key string, request *api.PromoteAutomationLiveBaselineRequest) (automationgate.CoveragePolicy, error) {
	invalid := api.ErrValidation("Provide a baseline name, promotion UUID, expected revision and target, suite UUID, and version 2 coverage/age policy.")
	p, err := automationgate.ParseCoveragePolicy(request.Policy)
	if err != nil || p.Version != 2 || !api.ValidAutomationLiveBaselineName(alias) || request.ExpectedRevision < 0 || request.ExpectedRevision == math.MaxInt64 {
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
	s := automationgate.Selection{Name: "promotion", CandidateID: request.SuiteID, Expected: automationgate.Target{Version: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: request.ExpectedDeploymentID}, Coverage: &p}
	if s.ValidateCandidate() != nil {
		return p, invalid
	}
	request.SuiteID = uuid.MustParse(request.SuiteID).String()
	request.ExpectedDeploymentID = uuid.MustParse(request.ExpectedDeploymentID).String()
	request.Policy, _ = json.Marshal(p)
	if int64(len(request.Policy)) > api.AutomationLiveGatePolicyMaxBytes {
		return p, api.ErrRequestBodyTooLarge(api.AutomationLiveGatePolicyMaxBytes, int64(len(request.Policy)))
	}
	return p, nil
}

func baselineReplay(receipt api.AutomationLiveBaseline, appID, name, alias, actor, key string, request api.PromoteAutomationLiveBaselineRequest, policy automationgate.CoveragePolicy) bool {
	return receipt.AppID == appID && receipt.Name == name && receipt.BaselineName == alias && receipt.ActorAccountID == actor && receipt.ActorAPIKeyID == key && receipt.Revision == request.ExpectedRevision+1 && receipt.Suite.ID == request.SuiteID && receipt.Suite.Version == request.ExpectedVersion && receipt.Suite.PublishedVersion == request.ExpectedPublishedVersion && receipt.Suite.DeploymentID == request.ExpectedDeploymentID && receipt.PolicyHash == policy.Hash()
}

func promotedBaseline(appID, name, alias, promotionID, actor, key string, request api.PromoteAutomationLiveBaselineRequest, policy automationgate.CoveragePolicy, prior api.AutomationLiveBaseline, view api.AutomationLiveSuite) (api.AutomationLiveBaseline, error) {
	now := time.Now().UTC()
	selection := automationgate.Selection{Name: name, BaselineID: view.ID, CandidateID: view.ID, Expected: automationgate.Target{Version: request.ExpectedVersion, PublishedVersion: request.ExpectedPublishedVersion, DeploymentID: request.ExpectedDeploymentID}, Coverage: &policy}
	result, err := automationgate.EvaluatePromotionAt(selection, compareLiveSuites(view, view), now)
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	if result.ExitCode != automationgate.ExitPassed || result.EvidenceAge == nil {
		return api.AutomationLiveBaseline{}, api.NewProblem(409, api.CodeConflict, "Suite cannot be promoted", "The suite must pass the explicit current target, required coverage and evidence-age policy. No baseline was changed.")
	}
	receipt := api.AutomationLiveBaseline{PromotionID: promotionID, AppID: appID, Name: name, BaselineName: alias, Revision: request.ExpectedRevision + 1, PreviousPromotionID: prior.PromotionID, ActorAccountID: actor, ActorAPIKeyID: key, PromotedAt: now, Suite: view.AutomationLiveSuiteSummary, Policy: append(json.RawMessage(nil), request.Policy...), PolicyHash: policy.Hash(), CasesPassed: result.CandidatePassed, OldestRunCreatedAt: result.EvidenceAge.OldestRunCreatedAt, EvidenceAgeSeconds: result.EvidenceAge.AgeSeconds}
	return receipt, nil
}

func cloneBaseline(receipt api.AutomationLiveBaseline) api.AutomationLiveBaseline {
	receipt.Policy = append(json.RawMessage(nil), receipt.Policy...)
	return receipt
}

func baselineLimitError(observed int64) error {
	return api.NewProblem(409, api.CodeConflict, "Named baseline limit reached", "Reuse an active baseline name or explicitly retire an unused name for this automation.").WithLimit(api.AutomationLiveBaselineLimit, observed).WithDocs("https://gregale.dev/docs/automations#baselines")
}
