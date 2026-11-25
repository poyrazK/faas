package routesplit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// Interval is how often the worker moves in-flight splits.
	Interval = 15 * time.Second
	// batch bounds the splits one pass moves.
	batch = 50
)

// Run moves in-flight splits until ctx ends.
func (s *Service) Run(ctx context.Context) error {
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		s.Step(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// Step moves every in-flight split one phase where it can.
func (s *Service) Step(ctx context.Context) {
	splits, err := s.Store.InFlightRouteSplits(ctx, batch)
	if err != nil {
		if ctx.Err() == nil {
			s.log().Warn("route split: list in-flight splits failed", "err", err)
		}
		return
	}
	for _, split := range splits {
		if ctx.Err() != nil {
			return
		}
		s.advance(ctx, split)
	}
}

func (s *Service) advance(ctx context.Context, split state.RouteSplit) {
	acct, err := s.Store.AccountByID(ctx, split.AccountID)
	if err != nil {
		s.log().Warn("route split: account unavailable", "split", split.ID, "err", err)
		return
	}
	switch split.Phase {
	case state.RouteSplitCreating:
		s.advanceCreating(ctx, acct, split)
	case state.RouteSplitDeploying:
		s.advanceDeploying(ctx, acct, split)
	case state.RouteSplitRuleLive:
		s.advanceRuleLive(ctx, acct, split)
	}
}

// advanceCreating rolls back a split apid stopped recording mid-create.
func (s *Service) advanceCreating(ctx context.Context, acct state.Account, split state.RouteSplit) {
	if s.now().Sub(split.UpdatedAt) < api.RouteSplitCreateTimeout {
		return
	}
	s.rollBack(ctx, acct, split, "the split stopped while creating the route's app")
}

func (s *Service) advanceDeploying(ctx context.Context, acct state.Account, split state.RouteSplit) {
	dep, err := s.Store.DeploymentByID(ctx, split.TargetDeploymentID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return
	}
	switch {
	case err != nil || dep.Status == state.DeployFailed || dep.Status == state.DeployCancelled:
		s.rollBack(ctx, acct, split, "the route's app did not deploy")
		return
	case dep.Status != state.DeployLive:
		if s.now().Sub(split.UpdatedAt) > api.RouteSplitDeployTimeout {
			s.rollBack(ctx, acct, split, "the route's app did not go live in time")
		}
		return
	}
	source, err := s.Store.AppByID(ctx, split.SourceAppID)
	if err != nil {
		return
	}
	target, err := s.Store.AppByID(ctx, split.TargetAppID)
	if err != nil {
		return
	}
	ids, prob := s.ensureRules(ctx, acct, source, target, split)
	if prob != nil {
		s.removeRules(ctx, acct, source, split)
		s.rollBack(ctx, acct, split, "could not route the path to the route's app: "+prob.Detail)
		return
	}
	split.EdgeRuleIDs, split.Phase = ids, state.RouteSplitRuleLive
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitDeploying); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record the rule", "split", split.ID, "err", err)
	}
}

// ensureRules creates the route rule on each of the source's hosts, reusing
// the ones an earlier pass made.
func (s *Service) ensureRules(ctx context.Context, acct state.Account, source, target state.App, split state.RouteSplit) ([]string, *api.Problem) {
	existing, err := s.Store.ListEdgeRulesForApp(ctx, source.ID)
	if err != nil {
		return nil, api.ErrCapacity("could not read the app's edge rules")
	}
	byHost := map[string]string{}
	for _, rule := range existing {
		if rule.Name == RuleName(split.ID) {
			byHost[rule.MatchHost] = rule.ID
		}
	}
	action, _ := json.Marshal(api.EdgeRuleRouteAction{TargetAppSlug: target.Slug})
	enabled := true
	var ids []string
	for _, host := range s.Host.AppHosts(ctx, source) {
		if id, ok := byHost[host]; ok {
			ids = append(ids, id)
			continue
		}
		req := api.CreateEdgeRuleRequest{
			MatchHost: host, MatchPath: split.PathGlob, Kind: "route", Action: action, Enabled: &enabled,
			Name:        RuleName(split.ID),
			Description: "Route split " + split.Route + " → " + target.Slug,
		}
		if split.Method != "" {
			req.MatchMethods = []string{split.Method}
		}
		id, prob := s.Host.CreateRule(ctx, acct, source, req)
		if prob != nil {
			return nil, prob
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, api.ErrValidation("the app serves on no host")
	}
	slices.Sort(ids)
	return ids, nil
}

func (s *Service) removeRules(ctx context.Context, acct state.Account, source state.App, split state.RouteSplit) *api.Problem {
	rules, err := s.Store.ListEdgeRulesForApp(ctx, source.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app's edge rules")
	}
	for _, rule := range rules {
		if rule.Name != RuleName(split.ID) {
			continue
		}
		if prob := s.Host.DeleteRule(ctx, acct, rule.ID); prob != nil && prob.Status != http.StatusNotFound {
			return prob
		}
	}
	return nil
}

// advanceRuleLive sizes a memory split's source app down, then finishes.
func (s *Service) advanceRuleLive(ctx context.Context, acct state.Account, split state.RouteSplit) {
	if split.Kind == routeresources.SplitMemory && !split.MainResized {
		source, err := s.Store.AppByID(ctx, split.SourceAppID)
		if err != nil {
			return
		}
		if prob := s.Host.ResizeApp(ctx, acct, source, split.MainRAMMB); prob != nil {
			split.Warning = truncate("the route moved, but the app was not sized down: " + prob.Detail)
		} else {
			split.MainResized = true
		}
	}
	split.Phase = state.RouteSplitDone
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitRuleLive); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record done", "split", split.ID, "err", err)
		return
	}
	s.log().Info("route split done", "split", split.ID, "app", split.SourceAppID, "resized", split.MainResized)
}

// rollBack deletes the route's app (found by ID, or by the reserved slug
// when apid stopped before recording it) and ends the split rolled back.
func (s *Service) rollBack(ctx context.Context, acct state.Account, split state.RouteSplit, reason string) {
	from := split.Phase
	if target, ok := s.target(ctx, split); ok {
		if prob := s.Host.DeleteApp(ctx, acct, target); prob != nil && prob.Status != http.StatusNotFound {
			s.log().Warn("route split: rollback could not delete the route's app", "split", split.ID, "detail", prob.Detail)
			return
		}
	}
	split.Phase, split.Error = state.RouteSplitRolledBack, truncate(reason)
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, from); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record rollback", "split", split.ID, "err", err)
		return
	}
	s.log().Info("route split rolled back", "split", split.ID, "app", split.SourceAppID, "reason", reason)
}

func (s *Service) target(ctx context.Context, split state.RouteSplit) (state.App, bool) {
	if split.TargetAppID != "" {
		app, err := s.Store.AppByID(ctx, split.TargetAppID)
		return app, err == nil && app.Status != state.AppDeleted
	}
	app, err := s.Store.AppBySlug(ctx, split.TargetSlug)
	if err != nil || app.AccountID != split.AccountID || app.Status == state.AppDeleted || app.CreatedAt.Before(split.CreatedAt) {
		return state.App{}, false
	}
	return app, true
}

// Undo reverses a finished split: the rules go first so traffic returns to
// the app, then the app gets its previous size back, then the route's app is
// deleted. A failed step leaves the split done, so undo can be retried.
func (s *Service) Undo(ctx context.Context, acct state.Account, source state.App, split state.RouteSplit) (state.RouteSplit, *api.Problem) {
	if split.Phase == state.RouteSplitUndone || split.Phase == state.RouteSplitRolledBack {
		return split, nil
	}
	if split.Phase != state.RouteSplitDone {
		return state.RouteSplit{}, api.NewProblem(http.StatusConflict, api.CodeRouteSplitInFlight, "Split in flight",
			"the split is still "+split.Phase+"; undo it once it is done")
	}
	if split.MainResized {
		// Grow first: once the rule goes, the app serves the route again.
		if prob := s.Host.ResizeApp(ctx, acct, source, split.PreviousRAMMB); prob != nil {
			return state.RouteSplit{}, prob
		}
	}
	if prob := s.removeRules(ctx, acct, source, split); prob != nil {
		return state.RouteSplit{}, prob
	}
	if target, ok := s.target(ctx, split); ok {
		if prob := s.Host.DeleteApp(ctx, acct, target); prob != nil && prob.Status != http.StatusNotFound {
			return state.RouteSplit{}, prob
		}
	}
	split.Phase = state.RouteSplitUndone
	out, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitDone)
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not record the undo")
	}
	s.log().Info("route split undone", "split", split.ID, "app", source.ID)
	return out, nil
}
