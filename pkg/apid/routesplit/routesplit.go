// Package routesplit applies a suggested route split (ADR-20261121170000
// amendment 2): POST /v1/apps/{slug}/route-splits creates a second app from
// the app's live build, and a worker routes the route to it once it is live
// and, for a memory split, sizes the app down. DELETE reverses a finished
// split.
//
// The split row is durable and every step is idempotent or fenced by a
// compare-and-set on the phase, so a restart or a second apid replica picks
// up where the last pass stopped.
package routesplit

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// Store is what the split reads and writes directly.
type Store interface {
	state.RouteSplitStore
	AccountByID(ctx context.Context, id string) (state.Account, error)
	AppByID(ctx context.Context, id string) (state.App, error)
	AppBySlug(ctx context.Context, slug string) (state.App, error)
	DeploymentByID(ctx context.Context, id string) (state.Deployment, error)
	ListAppSecrets(ctx context.Context, accountID, appID string) ([]state.AppSecret, error)
	ListEdgeRulesForApp(ctx context.Context, appID string) ([]state.EdgeRule, error)
}

// Host is the apid surface a split acts through, so the apps and rules it
// makes get the same validation, quotas, audit and notifications as ones a
// customer makes through the API.
type Host interface {
	// Suggest recomputes the app's split over the window; nil when none.
	Suggest(ctx context.Context, acct state.Account, app state.App, window time.Duration) (*routeresources.Split, error)
	// CreateApp creates the route's app beside source.
	CreateApp(ctx context.Context, acct state.Account, source state.App, slug string, ramMB int) (state.App, *api.Problem)
	// ResizeApp sets an app's RAM.
	ResizeApp(ctx context.Context, acct state.Account, app state.App, ramMB int) *api.Problem
	// DeleteApp deletes an app the split created.
	DeleteApp(ctx context.Context, acct state.Account, app state.App) *api.Problem
	// AppHosts lists the hostnames the app serves on.
	AppHosts(ctx context.Context, app state.App) []string
	// CreateRule and DeleteRule mutate edge rules.
	CreateRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest) (string, *api.Problem)
	DeleteRule(ctx context.Context, acct state.Account, ruleID string) *api.Problem
}

// Service applies, advances and undoes splits.
type Service struct {
	Store Store
	Host  Host
	Log   *slog.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// RuleName labels the edge rules of one split, so a retried pass finds the
// rules an earlier one made and undo removes them all.
func RuleName(splitID string) string { return "route-split:" + splitID }

// Apply checks the request against a fresh suggestion, records the split,
// creates the route's app and seeds its deployment.
func (s *Service) Apply(ctx context.Context, acct state.Account, app state.App, req api.CreateRouteSplitRequest) (state.RouteSplit, *api.Problem) {
	window, err := windowFor(req.Since)
	if err != nil {
		return state.RouteSplit{}, api.ErrValidation(err.Error())
	}
	if prob := s.unsupported(ctx, app); prob != nil {
		return state.RouteSplit{}, prob
	}
	suggestion, err := s.Host.Suggest(ctx, acct, app, window)
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not read the app's route resources")
	}
	if !Matches(suggestion, req) {
		return state.RouteSplit{}, changedProblem(suggestion)
	}
	if suggestion.RouteRAMOverPlan {
		return state.RouteSplit{}, api.NewProblem(422, api.CodeRouteSplitUnsupported, "Route needs a larger plan",
			fmt.Sprintf("the route needs %d MB, more than your plan's largest app; upgrade the plan first", suggestion.RouteRAMMB))
	}
	target, err := s.targetSlug(ctx, app.Slug, suggestion.PathGlob)
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not choose a name for the route's app")
	}
	previous := app.RAMMB
	if previous <= 0 {
		previous = suggestion.CurrentRAMMB
	}
	split, err := s.Store.CreateRouteSplit(ctx, state.RouteSplit{
		AccountID: app.AccountID, SourceAppID: app.ID, TargetSlug: target, Kind: suggestion.Kind,
		Route: suggestion.Route, Method: suggestion.Method, PathGlob: suggestion.PathGlob,
		RouteRAMMB: suggestion.RouteRAMMB, MainRAMMB: suggestion.MainRAMMB, PreviousRAMMB: previous,
	})
	if errors.Is(err, state.ErrRouteSplitInFlight) {
		return state.RouteSplit{}, api.NewProblem(409, api.CodeRouteSplitInFlight, "Split in flight",
			"this app already has a route split in flight; wait for it to finish (gregale app scale "+app.Slug+" --split-status)")
	}
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not record the split")
	}
	created, prob := s.Host.CreateApp(ctx, acct, app, target, split.RouteRAMMB)
	if prob != nil {
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not create the route's app: "+prob.Detail)
		return state.RouteSplit{}, prob
	}
	split.TargetAppID = created.ID
	if split, err = s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitCreating); err != nil {
		s.deleteTarget(ctx, acct, created)
		return state.RouteSplit{}, api.ErrCapacity("could not record the route's app")
	}
	dep, err := s.Store.SeedRouteSplitTarget(ctx, state.RouteSplitSeed{
		AccountID: app.AccountID, SplitID: split.ID, SourceAppID: app.ID, TargetAppID: created.ID,
	})
	if err != nil {
		s.deleteTarget(ctx, acct, created)
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not deploy the app's build to the route's app")
		if errors.Is(err, state.ErrNotFound) {
			return state.RouteSplit{}, api.NewProblem(409, api.CodeRouteSplitUnsupported, "No live build",
				"the app has no live deployment to copy; deploy it first")
		}
		return state.RouteSplit{}, api.ErrCapacity("could not deploy the app's build to the route's app")
	}
	split.TargetDeploymentID, split.Phase = dep.ID, state.RouteSplitDeploying
	split, err = s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitCreating)
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not record the route's deployment")
	}
	s.log().Info("route split applied", "split", split.ID, "app", app.ID, "target", created.ID, "kind", split.Kind)
	return split, nil
}

// Matches reports whether the request repeats the suggestion.
func Matches(s *routeresources.Split, req api.CreateRouteSplitRequest) bool {
	if s == nil {
		return false
	}
	kind := s.Kind
	if kind == "" {
		kind = routeresources.SplitMemory
	}
	return kind == req.Kind && s.Route == req.Route && strings.EqualFold(s.Method, req.Method) &&
		s.RouteRAMMB == req.RouteRAMMB && s.MainRAMMB == req.MainRAMMB
}

func changedProblem(s *routeresources.Split) *api.Problem {
	detail := "the app no longer has a split to suggest; read gregale metric resources again"
	if s != nil {
		detail = fmt.Sprintf("the suggestion is now %s (%s split, route %d MB, app %d MB); review it and apply again",
			s.Route, orMemory(s.Kind), s.RouteRAMMB, s.MainRAMMB)
	}
	return api.NewProblem(409, api.CodeRouteSplitChanged, "Suggestion changed", detail)
}

func orMemory(kind string) string {
	if kind == "" {
		return routeresources.SplitMemory
	}
	return kind
}

// unsupported refuses what the first slice does not carry: a project app,
// a preview, and managed or service bindings, which the route's app would
// not have.
func (s *Service) unsupported(ctx context.Context, app state.App) *api.Problem {
	refuse := func(detail string) *api.Problem {
		return api.NewProblem(422, api.CodeRouteSplitUnsupported, "Split not supported for this app", detail)
	}
	switch {
	case app.PreviewOfSlug != "":
		return refuse("a preview cannot be split")
	case app.ProjectID != "":
		return refuse("apps in a project are split in the project's source; split a standalone app, or move the route to its own service there")
	case len(app.Manifest.ServiceBindings) > 0:
		return refuse("the app has service bindings, which the route's app would not have yet")
	}
	secrets, err := s.Store.ListAppSecrets(ctx, app.AccountID, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app's bindings")
	}
	var managed []string
	for _, secret := range secrets {
		if secret.ManagedPostgresBindingID != "" || secret.ManagedObjectStorageCredentialID != "" {
			managed = append(managed, secret.Key)
		}
	}
	if len(managed) > 0 {
		return refuse("the app has managed bindings (" + strings.Join(managed, ", ") + "), which the route's app would not have yet")
	}
	return nil
}

func windowFor(since string) (time.Duration, error) {
	if since == "" {
		return api.RouteResourceWindowDefault, nil
	}
	d, err := time.ParseDuration(since)
	if err != nil || d < time.Minute || d > api.RouteResourceWindowMax {
		return 0, fmt.Errorf("since must be a duration between 1m and %s, such as 24h", api.RouteResourceWindowMax)
	}
	return d, nil
}

// targetSlug names the route's app <slug>-<segment>, with a random suffix
// when that is taken.
func (s *Service) targetSlug(ctx context.Context, slug, glob string) (string, error) {
	base := TargetSlug(slug, glob)
	name := base
	for range 4 {
		_, err := s.Store.AppBySlug(ctx, name)
		if errors.Is(err, state.ErrNotFound) {
			return name, nil
		}
		if err != nil {
			return "", err
		}
		name = withSuffix(base, randomSuffix())
	}
	return "", fmt.Errorf("route split: no free name for %s", base)
}

const slugMaxLen = 40

// TargetSlug names the route's app after the route's last static segment:
// shop + /exports/* → shop-exports.
func TargetSlug(slug, glob string) string {
	name := "heavy"
	segments := strings.Split(strings.Trim(glob, "/"), "/")
	for i := len(segments) - 1; i >= 0; i-- {
		seg := strings.ToLower(segments[i])
		clean := strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				return r
			}
			return -1
		}, seg)
		clean = strings.Trim(clean, "-")
		if clean != "" && !strings.Contains(seg, "*") {
			name = clean
			break
		}
	}
	return trimSlug(slug + "-" + name)
}

func withSuffix(base, suffix string) string {
	return trimSlug(base[:min(len(base), slugMaxLen-len(suffix)-1)]) + "-" + suffix
}

func trimSlug(s string) string {
	if len(s) > slugMaxLen {
		s = s[:slugMaxLen]
	}
	return strings.TrimRight(s, "-")
}

func randomSuffix() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// finish moves a split to a terminal phase, keeping a short reason.
func (s *Service) finish(ctx context.Context, split state.RouteSplit, from, phase, reason string) {
	split.Phase = phase
	if phase == state.RouteSplitRolledBack {
		split.Error = truncate(reason)
	} else {
		split.Warning = truncate(reason)
	}
	if _, err := s.Store.AdvanceRouteSplit(ctx, split, from); err != nil && !errors.Is(err, state.ErrConflict) {
		s.log().Warn("route split: could not record phase", "split", split.ID, "phase", phase, "err", err)
	}
}

func truncate(s string) string {
	if len(s) > 1000 {
		return s[:1000]
	}
	return s
}

func (s *Service) deleteTarget(ctx context.Context, acct state.Account, app state.App) {
	if prob := s.Host.DeleteApp(ctx, acct, app); prob != nil {
		s.log().Warn("route split: could not delete the route's app", "app", app.ID, "detail", prob.Detail)
	}
}

// Response renders a split for the API.
func Response(split state.RouteSplit, sourceSlug string) api.RouteSplitResponse {
	rules := split.EdgeRuleIDs
	if rules == nil {
		rules = []string{}
	}
	return api.RouteSplitResponse{
		ID: split.ID, Slug: sourceSlug, TargetSlug: split.TargetSlug, Kind: split.Kind,
		Route: split.Route, Method: split.Method, PathGlob: split.PathGlob,
		RouteRAMMB: split.RouteRAMMB, MainRAMMB: split.MainRAMMB, PreviousRAMMB: split.PreviousRAMMB,
		MainResized: split.MainResized, EdgeRuleIDs: rules, Phase: split.Phase,
		Warning: split.Warning, Error: split.Error,
		CreatedAt: split.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: split.UpdatedAt.UTC().Format(time.RFC3339),
	}
}
