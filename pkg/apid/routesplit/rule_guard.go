package routesplit

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A split checks the source app's edge rules when it is applied; once it
// is, rule changes are checked against it too (ADR-20261121170000
// amendment 5), so a later edit cannot quietly undo what the split relies
// on: its route rule, the block on the route's app's own hosts, no
// app-wide rule on the source, and no other rule deciding the moved path.

type actingKey struct{}

// Acting marks ctx as the split's own work: the rules a split creates and
// removes go through the edge-rule handlers, which skip the guard for it.
func Acting(ctx context.Context) context.Context {
	return context.WithValue(ctx, actingKey{}, true)
}

func acting(ctx context.Context) bool {
	v, _ := ctx.Value(actingKey{}).(bool)
	return v
}

// liveSplitLimit bounds the splits one app's rule change is checked
// against; an app has at most one split in flight and few finished ones.
const liveSplitLimit = 50

// Live reports whether the split still shapes traffic or is about to:
// anything but rolled back or undone.
func Live(split state.RouteSplit) bool {
	return split.Phase != state.RouteSplitRolledBack && split.Phase != state.RouteSplitUndone
}

// SplitIDOf returns the split a rule name belongs to.
func SplitIDOf(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, RuleNamePrefix)
	return id, ok && id != ""
}

// RuleChangeProblem refuses an edge-rule change that would undo a live
// split. before is nil for a new rule and after nil for a deleted one; both
// carry the rule's account and app.
func (s *Service) RuleChangeProblem(ctx context.Context, before, after *state.EdgeRule) *api.Problem {
	if acting(ctx) {
		return nil
	}
	if before != nil {
		if prob := s.ownedRuleProblem(ctx, *before); prob != nil {
			return prob
		}
	}
	if after == nil {
		return nil
	}
	if _, owned := SplitIDOf(after.Name); owned && (before == nil || before.Name != after.Name) {
		return api.ErrValidation("edge rule names starting with " + RuleNamePrefix + " belong to route splits; choose another name")
	}
	splits, err := s.Store.RouteSplitsForApp(ctx, after.AccountID, after.AppID, liveSplitLimit)
	if err != nil {
		return api.ErrCapacity("could not read the app's route splits")
	}
	for _, split := range splits {
		if !Live(split) {
			continue
		}
		if prob := afterProblem(*after, split); prob != nil {
			return prob
		}
	}
	return nil
}

// ownedRuleProblem refuses changing or removing a rule a live split made.
func (s *Service) ownedRuleProblem(ctx context.Context, rule state.EdgeRule) *api.Problem {
	id, owned := SplitIDOf(rule.Name)
	if !owned {
		return nil
	}
	split, err := s.Store.RouteSplit(ctx, rule.AccountID, id)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return api.ErrCapacity("could not read the rule's route split")
	}
	if !Live(split) {
		return nil
	}
	return conflict("the edge rule " + rule.Name + " is part of route split " + split.ID +
		"; undo the split instead (gregale app scale " + s.sourceSlug(ctx, split) + " --undo-split " + split.ID + ")")
}

// afterProblem refuses a source-app rule the split cannot run beside: the
// checks Apply makes before splitting, kept while the split is live.
func afterProblem(rule state.EdgeRule, split state.RouteSplit) *api.Problem {
	if _, owned := SplitIDOf(rule.Name); owned {
		return nil
	}
	if api.IsEdgeRuleAppHosts(rule.MatchHost) {
		return conflict("route split " + split.ID + " serves " + split.PathGlob +
			" from another app, where app-wide (@app) rules of this app do not run; give the rule the app's hosts instead of @app")
	}
	if !rule.Enabled || !globsMayOverlap(rule.MatchPath, split.PathGlob) {
		return nil
	}
	switch rule.Kind {
	case state.EdgeRuleKindRoute, state.EdgeRuleKindRewrite, state.EdgeRuleKindRedirect,
		state.EdgeRuleKindRespond, state.EdgeRuleKindAsync:
		return conflict("route split " + split.ID + " already decides where " + split.PathGlob +
			" goes; narrow the rule's path to exclude it, or undo the split first")
	case state.EdgeRuleKindThrottle:
		return conflict("route split " + split.ID + " serves " + split.PathGlob +
			" from another app, which would count its requests separately; narrow the rate limit's path to exclude it, or undo the split first")
	}
	return nil
}

func (s *Service) sourceSlug(ctx context.Context, split state.RouteSplit) string {
	if app, err := s.Store.AppByID(ctx, split.SourceAppID); err == nil && app.Slug != "" {
		return app.Slug
	}
	return "APP"
}

func conflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeRouteSplitRuleConflict, "Rule conflicts with a route split", detail)
}
