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
	// maxPages bounds one pass at maxPages*batch splits.
	maxPages = 40
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
	// Page through every in-flight split: splits that wait (a deployment
	// coming up, a rollback retrying) keep their updated_at, so one page
	// could otherwise hold the newest splits back forever.
	var after state.RouteSplitCursor
	for range maxPages {
		splits, err := s.Store.InFlightRouteSplits(ctx, after, batch)
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
		if len(splits) < batch {
			return
		}
		last := splits[len(splits)-1]
		after = state.RouteSplitCursor{UpdatedAt: last.UpdatedAt, ID: last.ID}
	}
}

// advance claims the split for this pass, so two apid replicas never run
// its side effects at once, then moves it one phase.
func (s *Service) advance(ctx context.Context, split state.RouteSplit) {
	if split.Phase == state.RouteSplitCreating && s.now().Sub(split.UpdatedAt) < api.RouteSplitCreateTimeout {
		return // Apply is still creating it.
	}
	claimed, ok, err := s.Store.ClaimRouteSplit(ctx, split, s.now().Add(api.RouteSplitLease))
	if err != nil || !ok {
		return
	}
	// A pass that leaves the phase alone ends its claim, so the next tick
	// rechecks the split instead of waiting out the lease.
	defer func() {
		if err := s.Store.ReleaseRouteSplit(context.WithoutCancel(ctx), claimed); err != nil {
			s.log().Warn("route split: release failed", "split", claimed.ID, "err", err)
		}
	}()
	split = claimed
	acct, err := s.actingAccount(ctx, split)
	if err != nil {
		s.log().Warn("route split: account unavailable", "split", split.ID, "err", err)
		return
	}
	switch split.Phase {
	case state.RouteSplitCreating:
		s.rollBack(ctx, acct, split, "the split stopped while creating the route's app")
	case state.RouteSplitDeploying:
		s.advanceDeploying(ctx, acct, split)
	case state.RouteSplitRuleLive:
		s.advanceRuleLive(ctx, acct, split)
	}
}

// actingAccount is the source app's resource account, the one the HTTP
// requests of the split act as; the split's own account when the source is
// gone.
func (s *Service) actingAccount(ctx context.Context, split state.RouteSplit) (state.Account, error) {
	if source, err := s.Store.AppByID(ctx, split.SourceAppID); err == nil {
		return s.Host.ResourceAccount(ctx, source)
	}
	return s.Store.AccountByID(ctx, split.AccountID)
}

func (s *Service) advanceDeploying(ctx context.Context, acct state.Account, split state.RouteSplit) {
	expired := s.now().Sub(split.UpdatedAt) > api.RouteSplitDeployTimeout
	dep, err := s.Store.DeploymentByID(ctx, split.TargetDeploymentID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		if expired {
			s.rollBack(ctx, acct, split, "the route's deployment could not be read")
		}
		return
	}
	switch {
	case err != nil || dep.Status == state.DeployFailed || dep.Status == state.DeployCancelled:
		s.rollBack(ctx, acct, split, "the route's app did not deploy")
		return
	case dep.Status != state.DeployLive:
		if expired {
			s.rollBack(ctx, acct, split, "the route's app did not go live in time")
		}
		return
	}
	source, err1 := s.Store.AppByID(ctx, split.SourceAppID)
	target, err2 := s.Store.AppByID(ctx, split.TargetAppID)
	if err1 != nil || err2 != nil || source.Status == state.AppDeleted || target.Status == state.AppDeleted {
		if errors.Is(err1, state.ErrNotFound) || errors.Is(err2, state.ErrNotFound) ||
			source.Status == state.AppDeleted || target.Status == state.AppDeleted || expired {
			s.rollBack(ctx, acct, split, "the app or the route's app is gone")
		}
		return
	}
	// Apply made the block; make sure it is whole (a host added since, a
	// rule removed) before any route rule.
	if prob := s.ensureBlock(ctx, acct, source, target, split); prob != nil {
		if transient(prob) && !expired {
			return // a later pass retries
		}
		s.rollBack(ctx, acct, split, "could not block direct access to the route's app: "+prob.Detail)
		return
	}
	ids, prob := s.ensureRules(ctx, acct, source, target, split)
	if prob != nil {
		if cleanup := s.removeRules(ctx, acct, source, split); cleanup != nil {
			return // retried on the next pass
		}
		s.rollBack(ctx, acct, split, "could not route the path to the route's app: "+prob.Detail)
		return
	}
	split.EdgeRuleIDs, split.Phase = ids, state.RouteSplitRuleLive
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitDeploying); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record the rule", "split", split.ID, "err", err)
	}
}

// ensureBlock denies every request to the route's app that arrives on its
// own hostnames. Routed requests keep the source's host, so the block never
// matches them, while the source's host rules still do.
func (s *Service) ensureBlock(ctx context.Context, acct state.Account, source, target state.App, split state.RouteSplit) *api.Problem {
	// The mark is what the gateway enforces on every host the app has, or
	// gets later (amendment 6); the host rules below cover a gateway that
	// predates it.
	if target.RouteOfAppID != source.ID {
		if err := s.Store.MarkRouteApp(ctx, target.ID, source.ID); err != nil {
			s.log().Warn("route split: could not mark the route's app", "split", split.ID, "app", target.ID, "err", err)
			if errors.Is(err, state.ErrNotFound) {
				// Gone, or no longer in the source's account: a retry cannot help.
				return api.NewProblem(422, api.CodeRouteSplitUnsupported, "Split not supported for this app",
					"the route's app is gone or belongs to another account")
			}
			return api.ErrCapacity("could not mark the route's app")
		}
	}
	have, prob := s.splitRules(ctx, target, split)
	if prob != nil {
		return prob
	}
	action, _ := json.Marshal(api.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0", "::/0"}})
	enabled := true
	for _, host := range s.Host.DirectHosts(ctx, target) {
		if _, ok := have[host]; ok {
			continue
		}
		_, prob := s.Host.CreateRule(ctx, acct, target, api.CreateEdgeRuleRequest{
			MatchHost: host, MatchPath: "/**", Kind: "ip", Action: action, Enabled: &enabled,
			Name:        RuleName(split.ID),
			Description: "Route split " + split.Route + ": this app serves the route through " + source.Slug + " only",
		})
		if prob != nil {
			return prob
		}
	}
	return nil
}

// ensureRules creates the route rule on each of the source's hosts, reusing
// the ones an earlier pass made.
func (s *Service) ensureRules(ctx context.Context, acct state.Account, source, target state.App, split state.RouteSplit) ([]string, *api.Problem) {
	byHost, prob := s.splitRules(ctx, source, split)
	if prob != nil {
		return nil, prob
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

// splitRules maps host to rule ID for the split's rules on source.
func (s *Service) splitRules(ctx context.Context, source state.App, split state.RouteSplit) (map[string]string, *api.Problem) {
	rules, err := s.Store.ListEdgeRulesForApp(ctx, source.ID)
	if err != nil {
		return nil, api.ErrCapacity("could not read the app's edge rules")
	}
	byHost := map[string]string{}
	for _, rule := range rules {
		if rule.Name == RuleName(split.ID) {
			byHost[rule.MatchHost] = rule.ID
		}
	}
	return byHost, nil
}

// removeRules deletes the split's rules and confirms none is left, so a
// refusal never reads as removed.
func (s *Service) removeRules(ctx context.Context, acct state.Account, source state.App, split state.RouteSplit) *api.Problem {
	byHost, prob := s.splitRules(ctx, source, split)
	if prob != nil {
		return prob
	}
	for _, id := range byHost {
		if prob := s.Host.DeleteRule(ctx, acct, source, id); prob != nil && prob.Status != http.StatusNotFound {
			return prob
		}
	}
	left, prob := s.splitRules(ctx, source, split)
	if prob != nil {
		return prob
	}
	if len(left) > 0 {
		return api.ErrCapacity("the split's edge rules could not be removed")
	}
	return nil
}

// advanceRuleLive sizes a memory split's source app down, then finishes. A
// transient resize failure is retried for RouteSplitResizeRetryWindow; a
// refusal, or one that outlasts the window, finishes with a warning.
func (s *Service) advanceRuleLive(ctx context.Context, acct state.Account, split state.RouteSplit) {
	if split.Kind == routeresources.SplitMemory && !split.MainResized {
		source, err := s.Store.AppByID(ctx, split.SourceAppID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return
		}
		prob := api.ErrCapacity("the app is gone")
		if err == nil {
			prob = s.Host.ResizeApp(ctx, acct, source, split.MainRAMMB)
		}
		switch {
		case prob == nil:
			split.MainResized = true
		case transient(prob) && err == nil && s.now().Sub(split.UpdatedAt) < api.RouteSplitResizeRetryWindow:
			return // a later pass retries
		default:
			split.Warning = truncate("the route moved, but the app was not sized down: " + prob.Detail)
		}
	}
	split.Phase = state.RouteSplitDone
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitRuleLive); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record done", "split", split.ID, "err", err)
		return
	}
	s.log().Info("route split done", "split", split.ID, "app", split.SourceAppID, "resized", split.MainResized)
}

func transient(prob *api.Problem) bool {
	return prob.Status >= 500 || prob.Status == http.StatusTooManyRequests || prob.Status == http.StatusConflict
}

// rollBack deletes the route's app (found by ID, or by the reserved slug
// when apid stopped before recording it) and ends the split rolled back. A
// delete that keeps failing past RouteSplitAbandonAfter ends the split
// anyway, naming the app, so the source can be split again.
func (s *Service) rollBack(ctx context.Context, acct state.Account, split state.RouteSplit, reason string) {
	from := split.Phase
	abandon := s.now().Sub(split.UpdatedAt) >= api.RouteSplitAbandonAfter
	// The rules go first, so the path never routes to a deleted app.
	if source, err := s.Store.AppByID(ctx, split.SourceAppID); err == nil && source.Status != state.AppDeleted {
		if prob := s.removeRules(ctx, acct, source, split); prob != nil {
			if !abandon {
				s.log().Warn("route split: rollback could not remove the rules", "split", split.ID, "detail", prob.Detail)
				return
			}
			reason += "; the route's edge rules could not be removed (" + prob.Detail + ")"
		}
	} else if err != nil && !errors.Is(err, state.ErrNotFound) && !abandon {
		return
	}
	if target, ok := s.target(ctx, acct, split); ok {
		prob := s.removeRules(ctx, acct, target, split)
		if prob == nil {
			prob = s.deleteApp(ctx, acct, target)
		}
		if prob != nil {
			if !abandon {
				s.log().Warn("route split: rollback could not delete the route's app", "split", split.ID, "detail", prob.Detail)
				return
			}
			reason += "; " + target.Slug + " could not be deleted (" + prob.Detail + "): delete it with gregale app delete " + target.Slug
		}
	}
	split.Phase, split.Error = state.RouteSplitRolledBack, truncate(reason)
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, from); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record rollback", "split", split.ID, "err", err)
		return
	}
	s.log().Info("route split rolled back", "split", split.ID, "app", split.SourceAppID, "reason", reason)
}

// target finds the route's app. Looked up by the reserved slug, it must
// belong to the split's account or the acting one and be newer than the
// split, so another customer's app with that name is never touched.
func (s *Service) target(ctx context.Context, acct state.Account, split state.RouteSplit) (state.App, bool) {
	if split.TargetAppID != "" {
		app, err := s.Store.AppByID(ctx, split.TargetAppID)
		return app, err == nil && app.Status != state.AppDeleted
	}
	app, err := s.Store.AppBySlug(ctx, split.TargetSlug)
	if err != nil || app.Status == state.AppDeleted || app.CreatedAt.Before(split.CreatedAt) ||
		(app.AccountID != split.AccountID && app.AccountID != acct.ID) {
		return state.App{}, false
	}
	return app, true
}

// Undo reverses a finished split: the app gets its previous size back
// first (it serves the route again once the rules go), then the rules and
// the route's app are deleted. A failed step leaves the split done, so undo
// can be retried.
func (s *Service) Undo(ctx context.Context, acct state.Account, source state.App, split state.RouteSplit) (state.RouteSplit, *api.Problem) {
	if split.Phase == state.RouteSplitUndone || split.Phase == state.RouteSplitRolledBack {
		return split, nil
	}
	if split.Phase != state.RouteSplitDone {
		return state.RouteSplit{}, api.NewProblem(http.StatusConflict, api.CodeRouteSplitInFlight, "Split in flight",
			"the split is still "+split.Phase+"; undo it once it is done")
	}
	// One undo at a time: a second request waits for the first's answer.
	claimed, ok, err := s.Store.ClaimRouteSplit(ctx, split, s.now().Add(api.RouteSplitLease))
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not read the split")
	}
	if !ok {
		return s.undoneOrBusy(ctx, split)
	}
	defer func() {
		if err := s.Store.ReleaseRouteSplit(context.WithoutCancel(ctx), claimed); err != nil {
			s.log().Warn("route split: release failed", "split", claimed.ID, "err", err)
		}
	}()
	split = claimed
	if split.MainResized {
		if prob := s.Host.ResizeApp(ctx, acct, source, split.PreviousRAMMB); prob != nil {
			return state.RouteSplit{}, prob
		}
	}
	if prob := s.removeRules(ctx, acct, source, split); prob != nil {
		return state.RouteSplit{}, prob
	}
	if target, ok := s.target(ctx, acct, split); ok {
		// Deleting an app keeps its rules: the block goes first.
		if prob := s.removeRules(ctx, acct, target, split); prob != nil {
			return state.RouteSplit{}, prob
		}
		if prob := s.deleteApp(ctx, acct, target); prob != nil {
			return state.RouteSplit{}, prob
		}
	}
	split.Phase = state.RouteSplitUndone
	out, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitDone)
	if errors.Is(err, state.ErrConflict) {
		return s.undoneOrBusy(ctx, split)
	}
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not record the undo")
	}
	s.log().Info("route split undone", "split", split.ID, "app", source.ID)
	return out, nil
}

// undoneOrBusy answers an undo that lost the race: the split as it is when
// the other request undid it, or 409 while that one still runs.
func (s *Service) undoneOrBusy(ctx context.Context, split state.RouteSplit) (state.RouteSplit, *api.Problem) {
	current, err := s.Store.RouteSplit(ctx, split.AccountID, split.ID)
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not read the split")
	}
	if current.Phase == state.RouteSplitUndone {
		return current, nil
	}
	return state.RouteSplit{}, api.NewProblem(http.StatusConflict, api.CodeRouteSplitInFlight, "Undo in progress",
		"another request is undoing this split; check it with gregale app scale --split-status")
}
