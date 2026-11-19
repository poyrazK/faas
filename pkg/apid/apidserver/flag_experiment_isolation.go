package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Layers, the environment holdout and the experiment a rule carries
// (ADR 20261119173702 §2).

var (
	errFlagsUnchanged       = errors.New("flags unchanged")
	errExperimentLayerFull  = errors.New("the layer has no free slice of that size")
	errExperimentNoLayer    = errors.New("no experiment layer has that key")
	errExperimentLayerInUse = errors.New("a running experiment holds a slice of the layer")
	errHoldoutWhileRunning  = errors.New("the holdout cannot change while an experiment runs")
)

// publishFlags applies mutate to the latest configuration and publishes it,
// retrying when another write won the race. errFlagsUnchanged from mutate
// returns the current version without a write.
func (s *server) publishFlags(ctx context.Context, scope state.FeatureFlagScope, actor string, mutate func(flags.Config) (flags.Config, error)) (int64, error) {
	store, ok := s.store.(state.FeatureFlagStore)
	if !ok {
		return 0, errors.New("feature flags unavailable")
	}
	for attempt := 0; attempt < 3; attempt++ {
		current, err := store.GetFeatureFlags(ctx, scope, 0)
		if err != nil {
			return 0, fmt.Errorf("read flags: %w", err)
		}
		next, err := mutate(current.Config)
		if errors.Is(err, errFlagsUnchanged) {
			return current.Version, nil
		}
		if err != nil {
			return 0, err
		}
		updated, err := store.UpdateFeatureFlags(ctx, state.FeatureFlagUpdate{Scope: scope, ExpectedVersion: current.Version, Config: next, Actor: actor})
		if errors.Is(err, state.ErrConflict) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("publish flags: %w", err)
		}
		return updated.Version, nil
	}
	return 0, fmt.Errorf("publish flags: %w", state.ErrConflict)
}

func experimentScope(x state.FlagExperiment) state.FeatureFlagScope {
	return state.FeatureFlagScope{AccountID: x.AccountID, ProjectID: x.ProjectID, EnvironmentID: x.EnvironmentID}
}

// needsRuleExperiment reports whether the SDKs must know of the experiment:
// it has a layer, subject units, or the environment holds customers out.
func needsRuleExperiment(x state.FlagExperiment, cfg flags.Config) bool {
	return x.Layer != "" || x.SubjectUnits() || cfg.Holdout != nil && cfg.Holdout.BasisPoints > 0
}

// startExperiment starts a draft. When the SDKs must know of it, it first
// publishes the rule's experiment, with the lowest free slice of its layer,
// and undoes that if the start fails.
func (s *server) startExperiment(ctx context.Context, store state.FlagExperimentStore, x state.FlagExperiment, weights map[string]int, actor string) (state.FlagExperiment, error) {
	var slice [2]int
	published := false
	_, err := s.publishFlags(ctx, experimentScope(x), actor, func(cfg flags.Config) (flags.Config, error) {
		if !needsRuleExperiment(x, cfg) {
			return cfg, errFlagsUnchanged
		}
		rx := &flags.RuleExperiment{Key: x.Key, Control: x.Control, Unit: orCustomer(x.Unit)}
		if x.Layer != "" {
			if !slices.ContainsFunc(cfg.Layers, func(l flags.Layer) bool { return l.Key == x.Layer }) {
				return cfg, errExperimentNoLayer
			}
			from, to, ok := flags.LayerSlice(flags.LayerSlices(cfg, x.Layer), x.Traffic)
			if !ok {
				return cfg, errExperimentLayerFull
			}
			rx.Layer, rx.From, rx.To, slice = x.Layer, from, to, [2]int{from, to}
		}
		next, ok := flags.SetRuleExperiment(cfg, x.FlagKey, x.RuleID, rx)
		if !ok {
			return cfg, errFlagExperimentRuleChanged
		}
		published = true
		return next, nil
	})
	if err != nil {
		return x, err
	}
	started, err := store.StartFlagExperiment(ctx, x.EnvironmentID, x.Key, weights, slice, time.Now(), api.FlagExperimentsMaxRunning)
	if err != nil && published {
		if cerr := s.clearRuleExperiment(ctx, x, actor); cerr != nil && s.log != nil {
			s.log.WarnContext(ctx, "flag experiments: undo rule experiment failed", "experiment", x.ID, "err", cerr)
		}
	}
	return started, err
}

func orCustomer(unit string) string {
	if unit == "" {
		return flags.ExperimentUnitCustomer
	}
	return unit
}

// clearRuleExperiment removes the experiment from its rule, so the rule
// serves everyone it matches again.
func (s *server) clearRuleExperiment(ctx context.Context, x state.FlagExperiment, actor string) error {
	_, err := s.publishFlags(ctx, experimentScope(x), actor, func(cfg flags.Config) (flags.Config, error) {
		for _, f := range cfg.Flags {
			for _, r := range f.Rules {
				if f.Key == x.FlagKey && r.ID == x.RuleID && r.Experiment != nil && r.Experiment.Key == x.Key {
					next, _ := flags.SetRuleExperiment(cfg, x.FlagKey, x.RuleID, nil)
					return next, nil
				}
			}
		}
		return cfg, errFlagsUnchanged
	})
	return err
}

func writeIsolationError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, errExperimentLayerFull):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_layer_full", "Layer full", err.Error()))
	case errors.Is(err, errExperimentNoLayer):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid experiment", err.Error()))
	case errors.Is(err, errExperimentLayerInUse), errors.Is(err, errHoldoutWhileRunning):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_running", "Experiment is running", err.Error()))
	case errors.Is(err, errFlagExperimentRuleChanged):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_rule_changed", "Rule changed", err.Error()))
	default:
		return false
	}
	return true
}

func randomSeed() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func layersResponse(cfg flags.Config) api.FlagExperimentLayerList {
	out := api.FlagExperimentLayerList{Layers: make([]api.FlagExperimentLayer, 0, len(cfg.Layers))}
	for _, l := range cfg.Layers {
		layer := api.FlagExperimentLayer{Key: l.Key, Slices: []api.FlagExperimentLayerSlice{}, Free: 10000}
		for _, f := range cfg.Flags {
			for _, r := range f.Rules {
				if x := r.Experiment; x != nil && x.Layer == l.Key {
					layer.Slices = append(layer.Slices, api.FlagExperimentLayerSlice{Experiment: x.Key, From: x.From, To: x.To})
					layer.Free -= x.To - x.From
				}
			}
		}
		slices.SortFunc(layer.Slices, func(a, b api.FlagExperimentLayerSlice) int { return a.From - b.From })
		out.Layers = append(out.Layers, layer)
	}
	return out
}

func (s *server) listFlagExperimentLayers(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	v, err := store.GetFeatureFlags(r.Context(), scope, 0)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, layersResponse(v.Config))
}

func (s *server) createFlagExperimentLayer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	var req api.FlagExperimentLayerRequest
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	if !flags.ValidKey(req.Key) {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid layer", "key is a flag key: ^[a-z][a-z0-9_-]{0,63}$"))
		return
	}
	var out flags.Config
	_, err := s.publishFlags(r.Context(), scope, acct.ID, func(cfg flags.Config) (flags.Config, error) {
		if slices.ContainsFunc(cfg.Layers, func(l flags.Layer) bool { return l.Key == req.Key }) {
			return cfg, state.ErrConflict
		}
		if len(cfg.Layers) >= api.FlagLayersMaxPerEnvironment {
			return cfg, state.ErrFlagExperimentLimit
		}
		cfg.Layers = append(slices.Clone(cfg.Layers), flags.Layer{Key: req.Key, Seed: randomSeed()})
		out = cfg
		return cfg, nil
	})
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Layer exists", "a layer with that key already exists"))
		return
	case errors.Is(err, state.ErrFlagExperimentLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "flag_experiment_layer_limit", "Layer limit reached", "the environment already has the most layers").
			WithLimit(api.FlagLayersMaxPerEnvironment, api.FlagLayersMaxPerEnvironment))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	s.audit.Emit(r.Context(), "flags.experiment_layer_created", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "layer": req.Key})
	writeJSON(w, http.StatusCreated, layersResponse(out))
}

func (s *server) deleteFlagExperimentLayer(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	key := r.PathValue("key")
	found := false
	_, err := s.publishFlags(r.Context(), scope, acct.ID, func(cfg flags.Config) (flags.Config, error) {
		i := slices.IndexFunc(cfg.Layers, func(l flags.Layer) bool { return l.Key == key })
		if i < 0 {
			return cfg, errFlagsUnchanged
		}
		found = true
		if len(flags.LayerSlices(cfg, key)) > 0 {
			return cfg, errExperimentLayerInUse
		}
		cfg.Layers = slices.Delete(slices.Clone(cfg.Layers), i, i+1)
		return cfg, nil
	})
	if writeIsolationError(w, err) {
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	if !found {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Layer not found", "no experiment layer has that key"))
		return
	}
	s.audit.Emit(r.Context(), "flags.experiment_layer_deleted", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "layer": key})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) getFlagExperimentHoldout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, store, ok := s.featureFlagScope(w, r, acct)
	if !ok {
		return
	}
	v, err := store.GetFeatureFlags(r.Context(), scope, 0)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	out := api.FlagExperimentHoldout{}
	if v.Holdout != nil {
		out.BasisPoints = v.Holdout.BasisPoints
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) setFlagExperimentHoldout(w http.ResponseWriter, r *http.Request, acct state.Account) {
	scope, _, store, ok := s.flagExperimentScope(w, r, acct)
	if !ok {
		return
	}
	var req api.FlagExperimentHoldout
	if !decodeJSONLimit(w, r, &req, 4<<10) {
		return
	}
	if req.BasisPoints < 0 || req.BasisPoints > api.FlagHoldoutMaxBasisPoints {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid holdout",
			fmt.Sprintf("basis_points is 0 to %d", api.FlagHoldoutMaxBasisPoints)).WithLimit(api.FlagHoldoutMaxBasisPoints, int64(req.BasisPoints)))
		return
	}
	list, err := store.ListFlagExperiments(r.Context(), scope.EnvironmentID)
	if err != nil {
		writeFlagExperimentError(w, err)
		return
	}
	running := slices.ContainsFunc(list, func(x state.FlagExperiment) bool { return x.Status == flags.ExperimentRunning })
	_, err = s.publishFlags(r.Context(), scope, acct.ID, func(cfg flags.Config) (flags.Config, error) {
		current := 0
		if cfg.Holdout != nil {
			current = cfg.Holdout.BasisPoints
		}
		if current == req.BasisPoints {
			return cfg, errFlagsUnchanged
		}
		if running {
			return cfg, errHoldoutWhileRunning
		}
		h := &flags.Holdout{Seed: randomSeed(), BasisPoints: req.BasisPoints}
		if cfg.Holdout != nil {
			h.Seed = cfg.Holdout.Seed
		}
		cfg.Holdout = h
		return cfg, nil
	})
	if writeIsolationError(w, err) {
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	s.audit.Emit(r.Context(), "flags.experiment_holdout_set", &acct.ID, map[string]any{"environment_id": scope.EnvironmentID, "basis_points": req.BasisPoints})
	writeJSON(w, http.StatusOK, req)
}

// holdoutReportMaxCustomers bounds the customers a holdout report reads.
const holdoutReportMaxCustomers = api.AnalyticsQueryMaxUnits

func (s *server) getFlagExperimentHoldoutReport(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return
	}
	scope := state.FeatureFlagScope{AccountID: acct.ID, ProjectID: sc.env.ProjectID, EnvironmentID: sc.env.ID}
	flagStore, ok1 := s.store.(state.FeatureFlagStore)
	analytics, ok2 := s.store.(state.AnalyticsCohortStore)
	if !ok1 || !ok2 {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	event := r.URL.Query().Get("event")
	from, until, err := holdoutReportRange(r, time.Now().UTC())
	if !flags.ValidEventName(event) || err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid report", "event is an event name; from and until are RFC 3339 times"))
		return
	}
	v, err := flagStore.GetFeatureFlags(r.Context(), scope, 0)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("feature flags unavailable"))
		return
	}
	report, err := holdoutReport(r.Context(), analytics, v.Holdout, sc.env.ID, event, from, until)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("product analytics unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func holdoutReportRange(r *http.Request, now time.Time) (time.Time, time.Time, error) {
	from, until := now.AddDate(0, 0, -30), now
	if raw := r.URL.Query().Get("from"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, until, err
		}
		from = t
	}
	if raw := r.URL.Query().Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return from, until, err
		}
		until = t
	}
	if !from.Before(until) {
		return from, until, errors.New("from is not before until")
	}
	return from, until, nil
}

// holdoutReport compares held-out customers with every other customer
// active in [from, until) on whether they sent event in that range.
func holdoutReport(ctx context.Context, store state.AnalyticsCohortStore, h *flags.Holdout, environmentID, event string, from, until time.Time) (api.FlagExperimentHoldoutReport, error) {
	out := api.FlagExperimentHoldoutReport{Event: event, From: from, Until: until,
		Held: api.FlagExperimentArm{Variant: "held"}, Exposed: api.FlagExperimentArm{Variant: "exposed"}}
	if h == nil || h.BasisPoints == 0 {
		return out, nil
	}
	out.BasisPoints = h.BasisPoints
	m := state.AnalyticsMatch{EnvironmentID: environmentID, Name: event, From: from, Until: until}
	did, err := store.AnalyticsCohortDid(ctx, m, 1, holdoutReportMaxCustomers)
	if err != nil {
		return out, err
	}
	didNot, err := store.AnalyticsCohortDidNot(ctx, m, from, holdoutReportMaxCustomers)
	if err != nil {
		return out, err
	}
	out.Truncated = len(did) >= holdoutReportMaxCustomers || len(didNot) >= holdoutReportMaxCustomers
	count := func(customers []string, converted bool) {
		for _, c := range customers {
			a := &out.Exposed
			if h.Held(c) {
				a = &out.Held
			}
			a.Exposed++
			if converted {
				a.Converted++
			}
		}
	}
	count(did, true)
	count(didNot, false)
	rate := func(a *api.FlagExperimentArm) float64 {
		if a.Exposed > 0 {
			a.Rate = float64(a.Converted) / float64(a.Exposed)
		}
		return a.Rate
	}
	ph, pe := rate(&out.Held), rate(&out.Exposed)
	out.Difference = pe - ph
	if out.Held.Exposed > 0 && out.Exposed.Exposed > 0 {
		half := 1.96 * math.Sqrt(ph*(1-ph)/float64(out.Held.Exposed)+pe*(1-pe)/float64(out.Exposed.Exposed))
		out.Low, out.High = out.Difference-half, out.Difference+half
	}
	return out, nil
}
