package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only trusted internal worker entry points and APID's background recovery
// worker stamp this key.
// Public JSON, headers and CLI flags cannot grant inventory permissions.
type bindingReleaseWorkerReadsKey struct{}

func bindingReleaseRequiredProblem() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeBindingReleaseRequired, "Binding release policy blocked traffic", "This scope requires fresh checked binding evidence before traffic can increase.").WithHint("Verify the exact deployment receiving traffic, then promote, advance its canary, or abort with both deployment_id and expected_predecessor_deployment_id. Recovery without exact selectors requires explicitly setting the release policy to off with its current revision and a reason.")
}

// Mirror the store's complete redistribution, including traffic gains caused
// by reducing the selected deployment. The locked write checks every fence.
func (s *server) bindingReleaseTrafficObservations(r *http.Request, acct state.Account, app state.App, target state.Deployment, percent int) ([]bindingPromotionObservation, *api.Problem) {
	store, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		return nil, api.ErrCapacity("binding release policies are unavailable")
	}
	rows, err := s.store.LiveDeployments(r.Context(), app.ID)
	if err != nil {
		return nil, api.ErrInternal("could not read traffic recipients")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	siblings := make([]struct {
		ID    string
		Prior int
	}, 0, len(rows))
	for _, d := range rows {
		if d.ID != target.ID {
			siblings = append(siblings, struct {
				ID    string
				Prior int
			}{d.ID, d.TrafficPercent})
		}
	}
	weights := state.RedistributeTraffic(siblings, 100-percent)
	proposed := map[string]int{target.ID: percent}
	for i, d := range siblings {
		proposed[d.ID] = weights[i]
	}
	var observations []bindingPromotionObservation
	for _, d := range rows {
		if proposed[d.ID] <= d.TrafficPercent {
			continue
		}
		contractCtx, problem := s.contractTrafficContext(r.Context(), app, d)
		if problem != nil {
			return nil, problem
		}
		*r = *r.WithContext(contractCtx)
		p, err := store.GetBindingReleasePolicy(r.Context(), acct.ID, app.ID, d.Scope)
		if err != nil {
			return nil, api.ErrCapacity("binding release policy could not be read")
		}
		if p.Mode != "enforce" {
			continue
		}
		age, err := time.ParseDuration(p.MaxVerificationAge)
		if err != nil {
			return nil, api.ErrCapacity("binding release policy is invalid")
		}
		observation, problem := s.observeBindingPromotion(r, acct, app, d, api.BindingPromotionRequest{RequireApplicationAck: p.RequireApplicationAck}, age)
		if problem != nil {
			return nil, problem
		}
		observations = append(observations, observation)
	}
	return observations, nil
}

func (s *server) withBindingReleaseTraffic(r *http.Request, acct state.Account, app state.App, target state.Deployment, percent int, write func(context.Context) error) (*api.Problem, error) {
	observations, problem := s.bindingReleaseTrafficObservations(r, acct, app, target, percent)
	if problem != nil {
		return problem, nil
	}
	return s.withBindingReleaseObservations(r, acct, app, observations, write)
}

func (s *server) withBindingReleaseObservations(r *http.Request, acct state.Account, app state.App, observations []bindingPromotionObservation, write func(context.Context) error) (*api.Problem, error) {
	var err error
	if len(observations) == 0 {
		err = write(r.Context())
	} else {
		fences := make([]state.BindingPromotionFence, 0, len(observations))
		for _, o := range observations {
			fences = append(fences, o.fence)
		}
		first := observations[0]
		err = s.managedPostgresBindings.GuardPromotion(r.Context(), acct.ID, app.ID, first.domain, first.store.BindingPromotionBackend(), func(ctx context.Context) error { return write(state.WithBindingReleaseFences(ctx, fences)) })
	}
	if p := routeRemovalBlockedProblem(err); p != nil {
		return p, err
	}
	if errors.Is(err, state.ErrCheckedRollbackRequired) {
		return api.NewProblem(http.StatusConflict, "rollback_operation_required", "Checked rollback in progress", "Wait for the exact rollback operation to publish traffic; generic promotion cannot complete its handoff."), err
	}
	if state.IsBindingReleaseRequired(err) {
		return bindingReleaseRequiredProblem(), err
	}
	if len(observations) > 0 && (errors.Is(err, state.ErrBindingPromotionChanged) || errors.Is(err, state.ErrBindingPromotionExpired) || errors.Is(err, managedpostgres.ErrConflict) || errors.Is(err, managedpostgres.ErrUnsupported)) {
		first := observations[0]
		return bindingPromotionWriteProblem(err, first.report, first.expiration), err
	}
	return nil, err
}
