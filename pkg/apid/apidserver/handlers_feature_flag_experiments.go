package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Flag experiments (ADR 20261115163113 §1, §5, §6), under
// /v1/projects/{slug}/environments/{environment}/experiments.

func (s *server) flagExperimentScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.FeatureFlagScope, state.FeatureFlagStore, state.FlagExperimentStore, bool) {
	scope, flagStore, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return scope, nil, nil, false
	}
	store, ok := s.store.(state.FlagExperimentStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("flag experiments unavailable"))
		return scope, nil, nil, false
	}
	return scope, flagStore, store, true
}

func writeFlagExperimentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "flag_experiment_not_found", "Experiment not found", "no experiment with this key in a state that allows the request"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_conflict", "Experiment conflict",
			"an experiment with this key exists, or another experiment already runs on this rule"))
	case errors.Is(err, state.ErrFlagExperimentLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_limit", "Experiment limit reached",
			"the environment already holds the maximum number of experiments, or of running ones").
			WithLimit(api.FlagExperimentsMaxRunning, api.FlagExperimentsMaxRunning))
	default:
		api.WriteProblem(w, api.ErrInternal("flag experiments unavailable"))
	}
}

func flagExperimentResponse(x state.FlagExperiment) api.FlagExperiment {
	out := api.FlagExperiment{Key: x.Key, Flag: x.FlagKey, RuleID: x.RuleID, Control: x.Control, MetricEvent: x.MetricEvent,
		MetricKind: x.MetricKind, Status: x.Status, MixingLift: x.MixingLift, Weights: x.Weights, CreatedBy: x.CreatedBy,
		CreatedAt: x.CreatedAt, StartedAt: x.StartedAt, StoppedAt: x.StoppedAt}
	if x.StartedAt != nil && !x.TelemetryCursor.IsZero() {
		cursor := x.TelemetryCursor
		out.AnalyzedUntil = &cursor
	}
	return out
}

// experimentRuleWeights checks the rule against the latest configuration and
// returns its flag's variant weights.
func experimentRuleWeights(ctx context.Context, store state.FeatureFlagStore, scope state.FeatureFlagScope, flagKey, ruleID, control string) (map[string]int, *api.Problem) {
	v, err := store.GetFeatureFlags(ctx, scope, 0)
	if err != nil {
		return nil, api.ErrInternal("feature flags unavailable")
	}
	_, f, err := flags.ExperimentRule(v.Config, flagKey, ruleID, control)
	if err != nil {
		return nil, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", err.Error())
	}
	return flags.ExperimentWeights(f), nil
}

func validFlagExperimentRequest(req api.FlagExperimentCreateRequest) *api.Problem {
	lift := api.FlagExperimentMixingLift
	if req.MixingLift != nil {
		lift = *req.MixingLift
	}
	switch {
	case !flags.ValidExperimentKey(req.Key):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", "key is 1 to 64 lowercase letters, digits and hyphens")
	case !flags.ValidEventName(req.MetricEvent):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", "metric_event is a conversion event name: ^[a-z][a-z0-9_.-]{0,63}$")
	case !(lift > 0 && lift <= 1):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", "mixing_lift is an absolute conversion-rate lift in (0, 1]")
	}
	return nil
}

func (s *server) listFlagExperiments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	list, err := store.ListFlagExperiments(r.Context(), scope.EnvironmentID)
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	out := api.FlagExperimentList{Experiments: make([]api.FlagExperiment, 0, len(list))}
	for _, x := range list {
		out.Experiments = append(out.Experiments, flagExperimentResponse(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createFlagExperiment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, flagStore, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	var req api.FlagExperimentCreateRequest
	if !decodeJSONLimit(w, r, &req, 16<<10) {
		return
	}
	if p := validFlagExperimentRequest(req); p != nil {
		api.WriteProblem(w, p)
		return
	}
	weights, p := experimentRuleWeights(r.Context(), flagStore, scope, req.Flag, req.RuleID, req.Control)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	lift := api.FlagExperimentMixingLift
	if req.MixingLift != nil {
		lift = *req.MixingLift
	}
	x, err := store.CreateFlagExperiment(r.Context(), state.FlagExperiment{AccountID: scope.AccountID, ProjectID: scope.ProjectID,
		EnvironmentID: scope.EnvironmentID, Key: req.Key, FlagKey: req.Flag, RuleID: req.RuleID, Control: req.Control,
		MetricEvent: req.MetricEvent, MixingLift: lift, Weights: weights, CreatedBy: acct.ID}, api.FlagExperimentsMaxPerEnvironment)
	if err == nil && req.Start {
		x, err = store.StartFlagExperiment(r.Context(), scope.EnvironmentID, req.Key, weights, time.Now(), api.FlagExperimentsMaxRunning)
	}
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "flags.experiment_created", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "experiment": x.Key, "flag": x.FlagKey, "status": x.Status})
	writeJSON(w, http.StatusCreated, flagExperimentResponse(x))
}

func (s *server) getFlagExperiment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	x, err := store.GetFlagExperiment(r.Context(), scope.EnvironmentID, r.PathValue("key"))
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, flagExperimentResponse(x))
}

func (s *server) startFlagExperiment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, flagStore, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	x, err := store.GetFlagExperiment(r.Context(), scope.EnvironmentID, r.PathValue("key"))
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	weights, p := experimentRuleWeights(r.Context(), flagStore, scope, x.FlagKey, x.RuleID, x.Control)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	if x, err = store.StartFlagExperiment(r.Context(), scope.EnvironmentID, x.Key, weights, time.Now(), api.FlagExperimentsMaxRunning); err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "flags.experiment_started", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "experiment": x.Key, "flag": x.FlagKey})
	writeJSON(w, http.StatusOK, flagExperimentResponse(x))
}

func (s *server) stopFlagExperiment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	x, err := store.StopFlagExperiment(r.Context(), scope.EnvironmentID, r.PathValue("key"), time.Now())
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "flags.experiment_stopped", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "experiment": x.Key, "flag": x.FlagKey})
	writeJSON(w, http.StatusOK, flagExperimentResponse(x))
}

func (s *server) deleteFlagExperiment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	deleted, err := store.DeleteFlagExperiment(r.Context(), scope.EnvironmentID, r.PathValue("key"))
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	if !deleted {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_running", "Experiment is running",
			"stop the experiment before deleting it, or no experiment has this key"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getFlagExperimentResults(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	x, err := store.GetFlagExperiment(r.Context(), scope.EnvironmentID, r.PathValue("key"))
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	arms, crossed, err := store.FlagExperimentArms(r.Context(), x.ID)
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, flagExperimentResultsResponse(x, arms, crossed))
}

// flagExperimentResults applies the ADR's statistics to an experiment's arms.
func flagExperimentResults(x state.FlagExperiment, arms []flags.Arm) flags.Results {
	return flags.ExperimentResults(flags.ResultsInput{
		Arms: arms, Control: x.Control, Weights: x.Weights, MixingLift: x.MixingLift,
		Alpha: api.FlagExperimentAlpha, SRMAlpha: api.FlagExperimentSRMAlpha,
		MinUnits: api.FlagExperimentMinUnitsPerArm, MinConversions: api.FlagExperimentMinConversionsPerArm,
		MinPValues: x.MinPValues,
	})
}

func flagExperimentResultsResponse(x state.FlagExperiment, arms []flags.Arm, crossed int64) api.FlagExperimentResults {
	res := flagExperimentResults(x, arms)
	arm := func(a flags.Arm) api.FlagExperimentArm {
		return api.FlagExperimentArm{Variant: a.Variant, Exposed: a.Exposed, Converted: a.Converted, Rate: a.Rate()}
	}
	out := api.FlagExperimentResults{Experiment: flagExperimentResponse(x), Control: arm(res.Control),
		SampleRatioPValue: res.SampleRatioP, SampleRatioMismatch: res.SampleRatioError, CrossedUnits: crossed,
		Variants: make([]api.FlagExperimentVariantResult, 0, len(res.Variants))}
	for _, v := range res.Variants {
		vr := api.FlagExperimentVariantResult{FlagExperimentArm: arm(v.Arm), Status: v.Status}
		if c := v.Comparison; c != nil {
			vr.Lift, vr.RelativeLift, vr.Low, vr.High, vr.PValue = &c.Lift, &c.RelativeLift, &c.Low, &c.High, &c.PValue
		}
		out.Variants = append(out.Variants, vr)
	}
	return out
}
