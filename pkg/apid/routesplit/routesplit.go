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
	"net/http"
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
	ListApps(ctx context.Context, accountID string) ([]state.App, error)
	ListEdgeRulesForApp(ctx context.Context, appID string) ([]state.EdgeRule, error)
}

// Binding kinds a split carries to the route's app (amendment 4).
const (
	BindingPostgres = "postgres"
	BindingBucket   = "bucket"
)

// Binding is one managed binding of the source app. The route's app gets
// its own binding like it: a login on the same database with the same
// access and variable, or a key on the same bucket with the same
// permission, variables and key scope.
type Binding struct {
	Kind string
	ID   string
	// Scope is the environment the binding's variables land in; the
	// route's app serves the default scope only.
	Scope string
	// Label names it in messages: the variable, and the database or bucket.
	Label string
	// Ready is false while the binding is still being set up or rotated.
	Ready bool
}

// Host is the apid surface a split acts through, so the apps and rules it
// makes get the same validation, quotas, audit and notifications as ones a
// customer makes through the API.
type Host interface {
	// ResourceAccount is the account apid acts as on app: for a company
	// app the organization's resource account, not its creator. Every
	// split step acts as it, so the rules and apps one step makes are the
	// ones another step can see and remove.
	ResourceAccount(ctx context.Context, app state.App) (state.Account, error)
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
	// DirectHosts are the match_host patterns that reach app on its own,
	// not through another app's route rule: its platform host and its
	// port-selector hosts. A split blocks them on the route's app.
	DirectHosts(ctx context.Context, app state.App) []string
	// RuleKindProblem is why acct may not create rules of kind, or nil.
	RuleKindProblem(acct state.Account, kind string) *api.Problem
	// CreateRule and DeleteRule mutate edge rules.
	CreateRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest) (string, *api.Problem)
	DeleteRule(ctx context.Context, acct state.Account, app state.App, ruleID string) *api.Problem
	// Bindings lists app's managed bindings (Postgres logins and bucket
	// keys) in every scope.
	Bindings(ctx context.Context, app state.App) ([]Binding, *api.Problem)
	// Bind gives target its own binding like b.
	Bind(ctx context.Context, acct state.Account, source, target state.App, b Binding) *api.Problem
	// ReleaseBindings deletes target's Postgres bindings and revokes the
	// bucket keys it borrowed, so deleting it leaves no live credential.
	ReleaseBindings(ctx context.Context, acct state.Account, target state.App) *api.Problem
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
func RuleName(splitID string) string { return RuleNamePrefix + splitID }

// applyBudget bounds Apply's work after the split is recorded; it stays
// under RouteSplitCreateTimeout so the worker never rolls back a split
// Apply is still creating.
const applyBudget = api.RouteSplitCreateTimeout - time.Minute

// RuleNamePrefix starts every split rule's name.
const RuleNamePrefix = "route-split:"

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
	bindings, prob := s.carriedBindings(ctx, app)
	if prob != nil {
		return state.RouteSplit{}, prob
	}
	// The block on the route's app's own hosts is an ip rule: check the
	// account may make one before anything is created.
	if prob := s.Host.RuleKindProblem(acct, string(state.EdgeRuleKindIP)); prob != nil {
		refused := *prob
		refused.Detail = "a split blocks direct access to the route's app with an ip edge rule: " + prob.Detail
		return state.RouteSplit{}, &refused
	}
	suggestion, err := s.Host.Suggest(ctx, acct, app, window)
	if err != nil {
		return state.RouteSplit{}, api.ErrCapacity("could not read the app's route resources")
	}
	if !Matches(suggestion, req) {
		return state.RouteSplit{}, changedProblem(suggestion)
	}
	if prob := s.overlapping(ctx, app, suggestion.PathGlob); prob != nil {
		return state.RouteSplit{}, prob
	}
	if suggestion.RouteRAMOverPlan {
		limits, _ := api.LimitsFor(acct.Plan)
		return state.RouteSplit{}, api.ErrPlanLimitRAM(limits, suggestion.RouteRAMMB)
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
	// The split is recorded: finish it even if the caller goes away, within
	// the time the worker leaves a creating split alone.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), applyBudget)
	defer cancel()
	created, split, prob := s.createTarget(ctx, acct, app, split)
	if prob != nil {
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not create the route's app: "+prob.Detail)
		return state.RouteSplit{}, prob
	}
	split.TargetAppID = created.ID
	recorded, err := s.Store.AdvanceRouteSplit(ctx, split, state.RouteSplitCreating)
	if err != nil {
		s.deleteTarget(ctx, acct, created, split)
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not record the route's app")
		return state.RouteSplit{}, api.ErrCapacity("could not record the route's app")
	}
	split = recorded
	// Block the route's app's own hosts before it has a deployment, so it
	// never answers there.
	if prob := s.ensureBlock(ctx, acct, app, created, split); prob != nil {
		s.deleteTarget(ctx, acct, created, split)
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not block direct access to the route's app: "+prob.Detail)
		return state.RouteSplit{}, prob
	}
	// Its own bindings before its first deployment, so the route boots with
	// the database and bucket variables it had in the app.
	for _, b := range bindings {
		if prob := s.Host.Bind(ctx, acct, app, created, b); prob != nil {
			s.deleteTarget(ctx, acct, created, split)
			s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not bind "+b.Label+" to the route's app: "+prob.Detail)
			refused := *prob
			refused.Detail = "could not give the route's app its own binding " + b.Label + ": " + prob.Detail
			return state.RouteSplit{}, &refused
		}
	}
	dep, err := s.Store.SeedRouteSplitTarget(ctx, state.RouteSplitSeed{
		AccountID: app.AccountID, SplitID: split.ID, SourceAppID: app.ID, TargetAppID: created.ID,
	})
	if err != nil {
		s.deleteTarget(ctx, acct, created, split)
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not deploy the app's build to the route's app")
		if errors.Is(err, state.ErrNotFound) {
			return state.RouteSplit{}, api.NewProblem(409, api.CodeRouteSplitUnsupported, "No live build",
				"the app has no live deployment to copy; deploy it first")
		}
		return state.RouteSplit{}, api.ErrCapacity("could not deploy the app's build to the route's app")
	}
	deploying := split
	deploying.TargetDeploymentID, deploying.Phase = dep.ID, state.RouteSplitDeploying
	recorded, err = s.Store.AdvanceRouteSplit(ctx, deploying, state.RouteSplitCreating)
	if err != nil {
		s.deleteTarget(ctx, acct, created, split)
		s.finish(ctx, split, state.RouteSplitCreating, state.RouteSplitRolledBack, "could not record the route's deployment")
		return state.RouteSplit{}, api.ErrCapacity("could not record the route's deployment")
	}
	split = recorded
	s.log().Info("route split applied", "split", split.ID, "app", app.ID, "target", created.ID, "kind", split.Kind)
	return split, nil
}

// createTarget creates the route's app, taking a suffixed name when another
// creator took the chosen one first.
func (s *Service) createTarget(ctx context.Context, acct state.Account, source state.App, split state.RouteSplit) (state.App, state.RouteSplit, *api.Problem) {
	base := TargetSlug(source.Slug, split.PathGlob)
	for attempt := 0; ; attempt++ {
		created, prob := s.Host.CreateApp(ctx, acct, source, split.TargetSlug, split.RouteRAMMB)
		if prob == nil || prob.Status != http.StatusConflict || attempt == 2 {
			return created, split, prob
		}
		renamed := split
		renamed.TargetSlug = withSuffix(base, randomSuffix())
		next, err := s.Store.AdvanceRouteSplit(ctx, renamed, state.RouteSplitCreating)
		if err != nil {
			return state.App{}, split, prob
		}
		split = next
	}
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

// unsupported refuses what a split cannot carry: a project app, a preview,
// a static egress IP, app-wide edge rules, a binding still being set up,
// and other apps that admit the app as a caller by name.
func (s *Service) unsupported(ctx context.Context, app state.App) *api.Problem {
	refuse := func(detail string) *api.Problem {
		return api.NewProblem(422, api.CodeRouteSplitUnsupported, "Split not supported for this app", detail)
	}
	switch {
	case app.PreviewOfSlug != "":
		return refuse("a preview cannot be split")
	case app.RouteOfAppID != "":
		return refuse("this app is the route's app of another split; split its source app instead")
	case app.ProjectID != "":
		return refuse("apps in a project are split in the project's source; split a standalone app, or move the route to its own service there")
	case app.StaticEgressIP != nil:
		return refuse("the app has a static egress IP, which belongs to one app; the route would call out from another address")
	}
	// Routed requests keep the app's host, so its host rules (deny, auth,
	// IP, rate limit) still run on them, and the split blocks the route's
	// app's own hostnames. App-wide ("@app") rules follow the serving app,
	// which for a routed request is the route's app, so they would not run.
	rules, err := s.Store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app's edge rules")
	}
	for _, rule := range rules {
		if api.IsEdgeRuleAppHosts(rule.MatchHost) {
			return refuse("the app has app-wide edge rules (" + ruleLabel(rule) + "), which would not run on the route once it moves; give them the app's hosts instead of @app")
		}
	}
	return s.namedCallers(ctx, app)
}

// namedCallers refuses a split when another app admits this one as a
// caller by name (allowed_service_callers or allowed_service_call_scopes):
// the route's app has its own name, so those apps would refuse its calls.
// A split never edits another app's security settings; the owner adds the
// route's app there, or drops the list.
func (s *Service) namedCallers(ctx context.Context, app state.App) *api.Problem {
	apps, err := s.Store.ListApps(ctx, app.AccountID)
	if err != nil {
		return api.ErrCapacity("could not read the account's apps")
	}
	var naming []string
	for _, other := range apps {
		if other.ID != app.ID && other.Status != state.AppDeleted && NamesCaller(other.Manifest, app.Slug) {
			naming = append(naming, other.Slug)
		}
	}
	if len(naming) == 0 {
		return nil
	}
	return api.NewProblem(422, api.CodeRouteSplitUnsupported, "Split not supported for this app",
		"these apps admit "+app.Slug+" as a caller by name and would refuse calls from the route's app: "+strings.Join(naming, ", ")+
			"; remove "+app.Slug+" from their allowed service callers (or let the route's app call them too once it exists) before splitting")
}

// NamesCaller reports whether manifest's caller allowlists name slug.
func NamesCaller(manifest state.AppManifest, slug string) bool {
	if manifest.AllowedServiceCallers != nil {
		for _, name := range *manifest.AllowedServiceCallers {
			if api.SameServiceName(strings.TrimSpace(name), slug) {
				return true
			}
		}
	}
	if manifest.AllowedServiceCallScopes != nil {
		for name := range *manifest.AllowedServiceCallScopes {
			if api.SameServiceName(strings.TrimSpace(name), slug) {
				return true
			}
		}
	}
	return false
}

// carriedBindings are the source's bindings the route's app gets its own of:
// those in the default scope, the one it serves. A binding still being set
// up or rotated refuses the split, so the copy is never of a half state.
func (s *Service) carriedBindings(ctx context.Context, app state.App) ([]Binding, *api.Problem) {
	all, prob := s.Host.Bindings(ctx, app)
	if prob != nil {
		return nil, prob
	}
	var carried []Binding
	for _, b := range all {
		if b.Scope != state.DefaultEnvScope {
			continue
		}
		if !b.Ready {
			return nil, api.NewProblem(409, api.CodeRouteSplitUnsupported, "Binding not ready",
				"the app's binding "+b.Label+" is still being set up or rotated; split once it is ready")
		}
		carried = append(carried, b)
	}
	return carried, nil
}

// overlapping refuses a split whose path another rule on the app could
// already decide: a rule that sends the request elsewhere or answers it
// (route, rewrite, redirect, respond, async) would shadow or reshape the
// split's route rule, and a throttle's bucket is kept per serving app, so
// the moved path would get a second, separate allowance. Rules of earlier
// splits are the app's own routes and are kept.
func (s *Service) overlapping(ctx context.Context, app state.App, glob string) *api.Problem {
	rules, err := s.Store.ListEdgeRulesForApp(ctx, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the app's edge rules")
	}
	for _, rule := range rules {
		if !rule.Enabled || strings.HasPrefix(rule.Name, RuleNamePrefix) || !globsMayOverlap(rule.MatchPath, glob) {
			continue
		}
		switch rule.Kind {
		case state.EdgeRuleKindRoute, state.EdgeRuleKindRewrite, state.EdgeRuleKindRedirect,
			state.EdgeRuleKindRespond, state.EdgeRuleKindAsync:
			return api.NewProblem(422, api.CodeRouteSplitUnsupported, "Split not supported for this app",
				"the edge rule "+ruleLabel(rule)+" already decides where "+glob+" goes; remove it or narrow its path before splitting")
		case state.EdgeRuleKindThrottle:
			return api.NewProblem(422, api.CodeRouteSplitUnsupported, "Split not supported for this app",
				"the rate limit "+ruleLabel(rule)+" covers "+glob+", and the route's app would count its requests separately; narrow its path to exclude "+glob+" before splitting")
		}
	}
	return nil
}

// globsMayOverlap reports whether two path globs could match one path. It
// compares the literal text before the first wildcard (without a trailing
// slash, since "/a/**" may match "/a"), so it may say yes for globs that
// never meet, but never no for ones that do.
func globsMayOverlap(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	pa, wildA := literalPrefix(a)
	pb, wildB := literalPrefix(b)
	switch {
	case !wildA && !wildB:
		return a == b
	case !wildA:
		return strings.HasPrefix(a, pb)
	case !wildB:
		return strings.HasPrefix(b, pa)
	}
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

func literalPrefix(glob string) (string, bool) {
	i := strings.IndexAny(glob, "*?[{")
	if i < 0 {
		return glob, false
	}
	return strings.TrimSuffix(glob[:i], "/"), true
}

func ruleLabel(rule state.EdgeRule) string {
	if rule.Name != "" {
		return rule.Name
	}
	return string(rule.Kind) + " " + rule.MatchPath
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
		if api.IsReservedAppSlug(name) {
			name = withSuffix(base, randomSuffix())
			continue
		}
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

// deleteTarget removes the split's rules on the route's app (deleting an
// app keeps its rules) and then the app, best-effort.
func (s *Service) deleteTarget(ctx context.Context, acct state.Account, app state.App, split state.RouteSplit) {
	if prob := s.removeRules(ctx, acct, app, split); prob != nil {
		s.log().Warn("route split: could not remove the route's app's rules", "app", app.ID, "detail", prob.Detail)
	}
	if prob := s.deleteApp(ctx, acct, app); prob != nil {
		s.log().Warn("route split: could not delete the route's app", "app", app.ID, "detail", prob.Detail)
	}
}

// deleteApp deletes an app the split made. A 404 counts as deleted only
// when the app really is gone, so a refusal that reads as "no such app" (an
// access check) never leaves a route's app running unnoticed.
func (s *Service) deleteApp(ctx context.Context, acct state.Account, app state.App) *api.Problem {
	// Its bindings go first: apid refuses to delete an app with live
	// Postgres bindings, and a borrowed bucket key must not outlive it.
	if prob := s.Host.ReleaseBindings(ctx, acct, app); prob != nil {
		return prob
	}
	prob := s.Host.DeleteApp(ctx, acct, app)
	if prob == nil || prob.Status != http.StatusNotFound {
		return prob
	}
	current, err := s.Store.AppByID(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && current.Status == state.AppDeleted) {
		return nil
	}
	return prob
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
