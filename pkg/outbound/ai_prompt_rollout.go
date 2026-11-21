package outbound

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prompt canaries and evaluations (ADR 20261114180740).

// AIPromptCanaryJudgement is one active prompt canary: Rule holds the live
// version's (baseline) and the candidate's counts and the route-canary
// thresholds.
type AIPromptCanaryJudgement struct {
	PromptID string
	Version  int
	Rule     AIRouteCanaryJudgement
}

// AIPromptCanaryStore reads active prompt canaries and rolls them back.
type AIPromptCanaryStore interface {
	ActiveAIPromptCanaries(ctx context.Context) ([]AIPromptCanaryJudgement, error)
	// RollBackAIPromptCanary rolls back the canary on version if it is
	// still active there, and reports whether it did.
	RollBackAIPromptCanary(ctx context.Context, promptID string, version int, reason string) (bool, error)
}

// evaluatePromptCanaries judges every active prompt canary once by the
// route-canary rule and returns how many it rolled back.
func (e *AIRouteCanaryEvaluator) evaluatePromptCanaries(ctx context.Context) (int, error) {
	judgements, err := e.Prompts.ActiveAIPromptCanaries(ctx)
	if err != nil {
		return 0, err
	}
	rolledBack := 0
	for _, judgement := range judgements {
		reason := judgement.Rule.rollbackReason()
		if reason == "" {
			continue
		}
		done, err := e.Prompts.RollBackAIPromptCanary(ctx, judgement.PromptID, judgement.Version, reason)
		if err != nil {
			return rolledBack, err
		}
		if done {
			rolledBack++
			e.Metrics.ObserveAIPromptCanaryRollback(reason)
			if e.Log != nil {
				e.Log.Info("outboundd: AI prompt canary rolled back", "prompt_id", judgement.PromptID, "version", judgement.Version, "reason", reason)
			}
		}
	}
	return rolledBack, nil
}

// AIPromptEvaluationJob is one claimed prompt evaluation. Canary is the
// stored canary request to start if it passes, or nil.
type AIPromptEvaluationJob struct {
	ID                 string
	AccountID          string
	PromptID           string
	Baseline           AIPromptVersion
	Candidate          AIPromptVersion
	Cases              []map[string]string
	JudgeIntegrationID string
	JudgeModel         string
	MaxLossRate        int
	Canary             []byte
}

// AIPromptEvaluationItem is one case's result. A status is 0 when the
// call got no response.
type AIPromptEvaluationItem struct {
	Case                    int
	Verdict                 string
	BaselineStatus          int
	CandidateStatus         int
	BaselineCostMillicents  int64
	CandidateCostMillicents int64
	BaselineTTFBMS          *int64
	CandidateTTFBMS         *int64
	BaselineOutputText      string
	CandidateOutputText     string
}

// AIPromptEvaluationStore hands prompt evaluations to the worker and keeps
// their results.
type AIPromptEvaluationStore interface {
	ClaimAIPromptEvaluation(ctx context.Context) (AIPromptEvaluationJob, bool, error)
	FinishAIPromptEvaluation(ctx context.Context, job AIPromptEvaluationJob, items []AIPromptEvaluationItem, errorCode string) (AIEvaluationVerdict, error)
}

// promptItemsAsRouteItems lets prompt items use the route evaluation pass
// rule and totals.
func promptItemsAsRouteItems(items []AIPromptEvaluationItem) []AIEvaluationItem {
	out := make([]AIEvaluationItem, 0, len(items))
	for _, item := range items {
		out = append(out, AIEvaluationItem{Verdict: item.Verdict, CandidateStatus: item.CandidateStatus,
			CandidateCostMillicents: item.CandidateCostMillicents, CandidateTTFBMS: item.CandidateTTFBMS,
			BaselineCostMillicents: item.BaselineCostMillicents, BaselineTTFBMS: item.BaselineTTFBMS})
	}
	return out
}

// AIPromptEvaluationWorker runs claimed prompt evaluations through Handler.
type AIPromptEvaluationWorker struct {
	Store    AIPromptEvaluationStore
	Handler  *Handler
	Interval time.Duration
	Log      *slog.Logger
	// CandidateFirst decides whether the judge sees the candidate's answer
	// as A; nil tosses a fair coin per case.
	CandidateFirst func() bool
}

// Run processes prompt evaluations until ctx ends, polling when none is
// pending.
func (w *AIPromptEvaluationWorker) Run(ctx context.Context) {
	interval := w.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		processed, err := w.ProcessOne(ctx)
		if err != nil && ctx.Err() == nil && w.Log != nil {
			w.Log.Warn("outboundd: AI prompt evaluation", "err", err)
		}
		if processed && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// ProcessOne runs at most one prompt evaluation and reports whether it
// claimed one.
func (w *AIPromptEvaluationWorker) ProcessOne(ctx context.Context) (bool, error) {
	job, ok, err := w.Store.ClaimAIPromptEvaluation(ctx)
	if err != nil || !ok {
		return false, err
	}
	items, errorCode := w.evaluate(ctx, job)
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	verdict, err := w.Store.FinishAIPromptEvaluation(ctx, job, items, errorCode)
	if err != nil {
		return true, err
	}
	switch {
	case errorCode != "":
		w.Handler.Metrics.ObserveAIEvaluation(api.OutboundAIEvaluationFailed)
	case verdict.Pass:
		w.Handler.Metrics.ObserveAIEvaluation(api.OutboundAIEvaluationPass)
	default:
		w.Handler.Metrics.ObserveAIEvaluation(api.OutboundAIEvaluationFail)
	}
	return true, nil
}

// promptEvaluationTarget is the integration and model one version's
// evaluation calls go to.
type promptEvaluationTarget struct {
	integration Integration
	model       string
}

// evaluate runs every case, a few at a time, and returns the items or the
// code the evaluation fails with.
func (w *AIPromptEvaluationWorker) evaluate(ctx context.Context, job AIPromptEvaluationJob) ([]AIPromptEvaluationItem, string) {
	principal := internalPrincipal{accountID: job.AccountID}
	baseline, ok := w.target(ctx, principal, job.Baseline)
	if !ok {
		return nil, "outbound_ai_evaluation_target_unavailable"
	}
	candidate, ok := w.target(ctx, principal, job.Candidate)
	if !ok {
		return nil, "outbound_ai_evaluation_target_unavailable"
	}
	judge, err := w.Handler.Resolver.Integration(ctx, job.JudgeIntegrationID)
	if err != nil || !internalAllowed(judge, principal) {
		return nil, "outbound_ai_evaluation_judge_unavailable"
	}
	items := make([]AIPromptEvaluationItem, len(job.Cases))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(api.OutboundAIEvaluationParallelCalls, len(job.Cases)) {
		wg.Go(func() {
			for index := range next {
				items[index] = w.evaluateCase(ctx, job, baseline, candidate, judge, index)
			}
		})
	}
	for index := range job.Cases {
		if ctx.Err() != nil {
			break
		}
		next <- index
	}
	close(next)
	wg.Wait()
	verdict := JudgeAIEvaluation(promptItemsAsRouteItems(items), job.MaxLossRate)
	if verdict.JudgeErrors*100 > api.OutboundAIEvaluationJudgeErrorMaxPercent*len(items) {
		return items, "outbound_ai_evaluation_judge_failed"
	}
	return items, ""
}

// target resolves a version's integration: its own, or its route's first
// target, whose model falls back to the version's.
func (w *AIPromptEvaluationWorker) target(ctx context.Context, principal internalPrincipal, version AIPromptVersion) (promptEvaluationTarget, bool) {
	integrationID, model := version.Target.IntegrationID, version.Target.Model
	if version.Target.RouteID != "" {
		if w.Handler.Routes == nil {
			return promptEvaluationTarget{}, false
		}
		route, err := w.Handler.Routes.Route(ctx, version.Target.RouteID)
		if err != nil || route.AccountID != principal.accountID || len(route.Targets) == 0 {
			return promptEvaluationTarget{}, false
		}
		integrationID = route.Targets[0].IntegrationID
		if route.Targets[0].Model != "" {
			model = route.Targets[0].Model
		}
	}
	integration, err := w.Handler.Resolver.Integration(ctx, integrationID)
	if err != nil || !internalAllowed(integration, principal) {
		return promptEvaluationTarget{}, false
	}
	return promptEvaluationTarget{integration: integration, model: model}, true
}

// promptEvaluationCall is one version's answer to one case.
type promptEvaluationCall struct {
	call    internalCallResult
	body    []byte
	ok      bool
	settled bool
}

func (w *AIPromptEvaluationWorker) callVersion(ctx context.Context, job AIPromptEvaluationJob, version AIPromptVersion,
	target promptEvaluationTarget, variables map[string]string) promptEvaluationCall {
	body, missing, err := renderPromptRequest(version, api.OutboundAIPromptCallRequest{Variables: variables})
	if missing != "" || err != nil {
		return promptEvaluationCall{}
	}
	translation, err := translateRouteRequest(target.integration, "/v1/chat/completions", "", body, target.model)
	if err != nil {
		return promptEvaluationCall{body: body}
	}
	attempt := replayAttempt{path: translation.path, rawQuery: translation.rawQuery, body: translation.body,
		translator: translation.translator, model: target.model}
	call := w.Handler.internalCall(ctx, internalPrincipal{accountID: job.AccountID, caller: AIUsageCallerEvaluation},
		target.integration.ID, attempt, api.OutboundAIEvaluationCallTimeoutSeconds*time.Second)
	return promptEvaluationCall{call: call, body: body, ok: call.succeeded(), settled: call.outcome.settled}
}

// evaluateCase runs one case on both versions and judges the answers. A
// failed candidate is a candidate error; a failed baseline against a
// working candidate is a win.
func (w *AIPromptEvaluationWorker) evaluateCase(ctx context.Context, job AIPromptEvaluationJob, baselineTarget, candidateTarget promptEvaluationTarget,
	judge Integration, index int) AIPromptEvaluationItem {
	variables := job.Cases[index]
	baseline := w.callVersion(ctx, job, job.Baseline, baselineTarget, variables)
	candidate := w.callVersion(ctx, job, job.Candidate, candidateTarget, variables)
	item := AIPromptEvaluationItem{Case: index, BaselineStatus: baseline.call.status, CandidateStatus: candidate.call.status,
		BaselineTTFBMS: baseline.call.ttfbMS, CandidateTTFBMS: candidate.call.ttfbMS}
	if baseline.settled {
		item.BaselineCostMillicents = baseline.call.outcome.cost
	}
	if candidate.settled {
		item.CandidateCostMillicents = candidate.call.outcome.cost
	}
	if baseline.ok {
		item.BaselineOutputText = redactedOutputText(baseline.call.body, baseline.call.contentType)
	}
	if candidate.ok {
		item.CandidateOutputText = redactedOutputText(candidate.call.body, candidate.call.contentType)
	}
	switch {
	case !candidate.ok:
		item.Verdict = api.OutboundAIEvaluationItemCandidateError
	case !baseline.ok:
		item.Verdict = api.OutboundAIEvaluationItemWin
	default:
		item.Verdict = judgeAnswers(ctx, w.Handler, job.AccountID, judge, job.JudgeModel, moderationText(baseline.body),
			item.BaselineOutputText, item.CandidateOutputText, w.CandidateFirst)
	}
	return item
}
