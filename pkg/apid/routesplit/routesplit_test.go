package routesplit_test

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routesplit"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeHost records what the split asked apid to do and acts on the store.
type fakeHost struct {
	store   *state.MemStore
	split   *routeresources.Split
	resized map[string]int
	deleted map[string]bool
	ruleErr *api.Problem
	// ruleErrKind limits ruleErr to rules of one kind.
	ruleErrKind string
	// kindErr refuses every rule kind up front, as a plan gate does.
	kindErr   *api.Problem
	resizeErr *api.Problem
	// deleteNotFound answers 404 without deleting, as a refused access
	// check does.
	deleteNotFound bool
}

func (h *fakeHost) ResourceAccount(ctx context.Context, app state.App) (state.Account, error) {
	return h.store.AccountByID(ctx, app.AccountID)
}

func (h *fakeHost) Suggest(context.Context, state.Account, state.App, time.Duration) (*routeresources.Split, error) {
	return h.split, nil
}

func (h *fakeHost) CreateApp(ctx context.Context, acct state.Account, source state.App, slug string, ramMB int) (state.App, *api.Problem) {
	app, err := h.store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: slug, Status: state.AppActive, Type: source.Type, RAMMB: ramMB, MaxConcurrency: 1})
	if errors.Is(err, state.ErrConflict) {
		return state.App{}, api.NewProblem(http.StatusConflict, "slug_taken", "Conflict", err.Error())
	}
	if err != nil {
		return state.App{}, api.ErrCapacity(err.Error())
	}
	return app, nil
}

func (h *fakeHost) ResizeApp(_ context.Context, _ state.Account, app state.App, ramMB int) *api.Problem {
	if h.resizeErr != nil {
		return h.resizeErr
	}
	h.resized[app.ID] = ramMB
	return nil
}

func (h *fakeHost) DeleteApp(ctx context.Context, _ state.Account, app state.App) *api.Problem {
	if h.deleteNotFound {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app")
	}
	h.deleted[app.ID] = true
	if err := h.store.DeleteApp(ctx, app.ID); err != nil {
		return api.ErrCapacity(err.Error())
	}
	return nil
}

func (h *fakeHost) AppHosts(_ context.Context, app state.App) []string {
	return []string{app.Slug + ".gregale.dev", "shop.example.com"}
}

func (h *fakeHost) DirectHosts(_ context.Context, app state.App) []string {
	return []string{app.Slug + ".gregale.dev", app.Slug + "--*.gregale.dev"}
}

func (h *fakeHost) CreateRule(ctx context.Context, acct state.Account, app state.App, req api.CreateEdgeRuleRequest) (string, *api.Problem) {
	if h.ruleErr != nil && (h.ruleErrKind == "" || h.ruleErrKind == req.Kind) {
		return "", h.ruleErr
	}
	rule, err := h.store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: acct.ID, AppID: app.ID, MatchHost: req.MatchHost, MatchPath: req.MatchPath, MatchMethods: req.MatchMethods,
		Enabled: true, Kind: state.EdgeRuleKind(req.Kind), Name: req.Name,
	})
	if err != nil {
		return "", api.ErrCapacity(err.Error())
	}
	return rule.ID, nil
}

func (h *fakeHost) RuleKindProblem(state.Account, string) *api.Problem { return h.kindErr }

func (h *fakeHost) DeleteRule(ctx context.Context, _ state.Account, _ state.App, ruleID string) *api.Problem {
	if err := h.store.DeleteEdgeRule(ctx, ruleID); err != nil {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", err.Error())
	}
	return nil
}

type fixture struct {
	ctx    context.Context
	store  *state.MemStore
	host   *fakeHost
	svc    *routesplit.Service
	acct   state.Account
	source state.App
	clock  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "split-"+uuid.NewString()[:8]+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 1024, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	live, err := store.CreateDeployment(ctx, state.Deployment{AppID: source.ID, Scope: "default", Kind: state.DeploymentKindImage, ImageDigest: "sha256:shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, live.ID, "/shop.ext4", "layers/shop.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, live.ID); err != nil {
		t.Fatal(err)
	}
	f := &fixture{ctx: ctx, store: store, acct: acct, source: source, clock: time.Now()}
	f.host = &fakeHost{store: store, resized: map[string]int{}, deleted: map[string]bool{},
		split: &routeresources.Split{Kind: routeresources.SplitMemory, Route: "GET /exports/{id}", Method: "GET", PathGlob: "/exports/*",
			RouteRAMMB: 1920, CurrentRAMMB: 1024, MainRAMMB: 256}}
	f.svc = &routesplit.Service{Store: store, Host: f.host, Now: func() time.Time { return f.clock }}
	return f
}

func (f *fixture) request() api.CreateRouteSplitRequest {
	s := f.host.split
	return api.CreateRouteSplitRequest{Kind: s.Kind, Route: s.Route, Method: s.Method, RouteRAMMB: s.RouteRAMMB, MainRAMMB: s.MainRAMMB}
}

func (f *fixture) read(t *testing.T, id string) state.RouteSplit {
	t.Helper()
	split, err := f.store.RouteSplit(f.ctx, f.acct.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return split
}

func TestApplyRunsToDoneAndUndoes(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if split.Phase != state.RouteSplitDeploying || split.TargetSlug != "shop-exports" || split.PreviousRAMMB != 1024 {
		t.Fatalf("applied split = %+v", split)
	}
	target, err := f.store.AppByID(f.ctx, split.TargetAppID)
	if err != nil || target.RAMMB != 1920 {
		t.Fatalf("route's app = %+v, %v", target, err)
	}

	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDeploying {
		t.Fatalf("before the deployment is live the split is %s", got.Phase)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	got := f.read(t, split.ID)
	if got.Phase != state.RouteSplitRuleLive || len(got.EdgeRuleIDs) != 2 {
		t.Fatalf("after go-live the split is %+v", got)
	}
	rules, _ := f.store.ListEdgeRulesForApp(f.ctx, f.source.ID)
	for _, rule := range rules {
		if rule.MatchPath != "/exports/*" || len(rule.MatchMethods) != 1 || rule.MatchMethods[0] != "GET" || rule.Name != routesplit.RuleName(split.ID) {
			t.Fatalf("rule = %+v", rule)
		}
	}
	f.svc.Step(f.ctx)
	got = f.read(t, split.ID)
	if got.Phase != state.RouteSplitDone || !got.MainResized || f.host.resized[f.source.ID] != 256 {
		t.Fatalf("finished split = %+v, resized %v", got, f.host.resized)
	}

	undone, prob := f.svc.Undo(f.ctx, f.acct, f.source, got)
	if prob != nil {
		t.Fatalf("undo: %+v", prob)
	}
	if undone.Phase != state.RouteSplitUndone || f.host.resized[f.source.ID] != 1024 || !f.host.deleted[target.ID] {
		t.Fatalf("undone = %+v, resized %v, deleted %v", undone, f.host.resized, f.host.deleted)
	}
	if rules, _ := f.store.ListEdgeRulesForApp(f.ctx, f.source.ID); len(rules) != 0 {
		t.Fatalf("rules left after undo: %+v", rules)
	}
	if rules, _ := f.store.ListEdgeRulesForApp(f.ctx, target.ID); len(rules) != 0 {
		t.Fatalf("block rules left on the deleted route's app: %+v", rules)
	}
}

func TestCPUSplitKeepsTheAppsSize(t *testing.T) {
	f := newFixture(t)
	f.host.split = &routeresources.Split{Kind: routeresources.SplitCPU, Route: "POST /render", Method: "POST", PathGlob: "/render",
		RouteRAMMB: 1024, CurrentRAMMB: 1024, MainRAMMB: 1024}
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.svc.Step(f.ctx)
	got := f.read(t, split.ID)
	if got.Phase != state.RouteSplitDone || got.MainResized || len(f.host.resized) != 0 {
		t.Fatalf("cpu split = %+v, resized %v", got, f.host.resized)
	}
}

func TestApplyRefuses(t *testing.T) {
	f := newFixture(t)
	changed := f.request()
	changed.MainRAMMB = 512
	if _, prob := f.svc.Apply(f.ctx, f.acct, f.source, changed); prob == nil || prob.Code != api.CodeRouteSplitChanged {
		t.Fatalf("changed suggestion: %+v", prob)
	}
	if _, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request()); prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if _, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request()); prob == nil || prob.Code != api.CodeRouteSplitInFlight {
		t.Fatalf("second apply: %+v", prob)
	}
	project := f.source
	project.ProjectID = uuid.NewString()
	if _, prob := f.svc.Apply(f.ctx, f.acct, project, f.request()); prob == nil || prob.Code != api.CodeRouteSplitUnsupported {
		t.Fatalf("project app: %+v", prob)
	}
}

func TestManagedBindingsAreRefused(t *testing.T) {
	f := newFixture(t)
	secret := state.AppSecret{AccountID: f.acct.ID, AppID: f.source.ID, Key: "DATABASE_URL", ManagedPostgresBindingID: uuid.NewString()}
	f.svc.Store = bindingStore{Mem: f.store, secrets: []state.AppSecret{secret}}
	if _, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request()); prob == nil || prob.Code != api.CodeRouteSplitUnsupported {
		t.Fatalf("managed binding: %+v", prob)
	}
}

// bindingStore reports a managed binding the MemStore cannot set up here and
// forwards the rest of routesplit.Store.
type bindingStore struct {
	Mem     *state.MemStore
	secrets []state.AppSecret
}

func (s bindingStore) ListAppSecrets(context.Context, string, string) ([]state.AppSecret, error) {
	return s.secrets, nil
}

func (s bindingStore) CreateRouteSplit(ctx context.Context, split state.RouteSplit) (state.RouteSplit, error) {
	return s.Mem.CreateRouteSplit(ctx, split)
}

func (s bindingStore) RouteSplit(ctx context.Context, accountID, id string) (state.RouteSplit, error) {
	return s.Mem.RouteSplit(ctx, accountID, id)
}

func (s bindingStore) RouteSplitsForApp(ctx context.Context, accountID, appID string, limit int) ([]state.RouteSplit, error) {
	return s.Mem.RouteSplitsForApp(ctx, accountID, appID, limit)
}

func (s bindingStore) InFlightRouteSplits(ctx context.Context, after state.RouteSplitCursor, limit int) ([]state.RouteSplit, error) {
	return s.Mem.InFlightRouteSplits(ctx, after, limit)
}

func (s bindingStore) AdvanceRouteSplit(ctx context.Context, split state.RouteSplit, from string) (state.RouteSplit, error) {
	return s.Mem.AdvanceRouteSplit(ctx, split, from)
}

func (s bindingStore) ClaimRouteSplit(ctx context.Context, split state.RouteSplit, until time.Time) (state.RouteSplit, bool, error) {
	return s.Mem.ClaimRouteSplit(ctx, split, until)
}

func (s bindingStore) ReleaseRouteSplit(ctx context.Context, claimed state.RouteSplit) error {
	return s.Mem.ReleaseRouteSplit(ctx, claimed)
}

func (s bindingStore) SeedRouteSplitTarget(ctx context.Context, seed state.RouteSplitSeed) (state.Deployment, error) {
	return s.Mem.SeedRouteSplitTarget(ctx, seed)
}

func (s bindingStore) AccountByID(ctx context.Context, id string) (state.Account, error) {
	return s.Mem.AccountByID(ctx, id)
}

func (s bindingStore) AppByID(ctx context.Context, id string) (state.App, error) {
	return s.Mem.AppByID(ctx, id)
}

func (s bindingStore) AppBySlug(ctx context.Context, slug string) (state.App, error) {
	return s.Mem.AppBySlug(ctx, slug)
}

func (s bindingStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	return s.Mem.DeploymentByID(ctx, id)
}

func (s bindingStore) ListEdgeRulesForApp(ctx context.Context, appID string) ([]state.EdgeRule, error) {
	return s.Mem.ListEdgeRulesForApp(ctx, appID)
}

func TestDeployTimeoutRollsBack(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	f.clock = f.clock.Add(api.RouteSplitDeployTimeout + time.Minute)
	f.svc.Step(f.ctx)
	got := f.read(t, split.ID)
	if got.Phase != state.RouteSplitRolledBack || got.Error == "" || !f.host.deleted[split.TargetAppID] {
		t.Fatalf("timed-out split = %+v, deleted %v", got, f.host.deleted)
	}
	if _, prob := f.svc.Undo(f.ctx, f.acct, f.source, got); prob != nil {
		t.Fatalf("undo of a rolled-back split: %+v", prob)
	}
}

// A rollback removes rules an earlier pass made before it deletes the
// route's app, so the path never routes to a deleted app.
func TestRollbackRemovesRules(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if _, err := f.store.CreateEdgeRule(f.ctx, state.CreateEdgeRuleParams{AccountID: f.acct.ID, AppID: f.source.ID,
		MatchHost: "shop.gregale.dev", MatchPath: "/exports/*", Enabled: true, Kind: state.EdgeRuleKind("route"), Name: routesplit.RuleName(split.ID)}); err != nil {
		t.Fatal(err)
	}
	f.clock = f.clock.Add(api.RouteSplitDeployTimeout + time.Minute)
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitRolledBack {
		t.Fatalf("split = %+v", got)
	}
	if rules, _ := f.store.ListEdgeRulesForApp(f.ctx, f.source.ID); len(rules) != 0 {
		t.Fatalf("rules left after rollback: %+v", rules)
	}
	if rules, _ := f.store.ListEdgeRulesForApp(f.ctx, split.TargetAppID); len(rules) != 0 {
		t.Fatalf("block rules left after rollback: %+v", rules)
	}
}

// The route's app runs under the source's settings (here its egress
// allowlist) but scales to zero.
func TestTargetInheritsSettings(t *testing.T) {
	f := newFixture(t)
	allow := []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")}
	one := 1
	source, err := f.store.UpdateApp(f.ctx, f.source.ID, state.UpdateAppParams{
		EgressAllowlist: &allow, SetEgressAllowlist: true, MinInstances: &one, SetMinInstances: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	split, prob := f.svc.Apply(f.ctx, f.acct, source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	target, err := f.store.AppByID(f.ctx, split.TargetAppID)
	if err != nil {
		t.Fatal(err)
	}
	if len(target.EgressAllowlist) != 1 || target.EgressAllowlist[0] != allow[0] {
		t.Fatalf("route's app egress = %v", target.EgressAllowlist)
	}
	if target.RAMMB != 1920 || target.MinInstances != 0 {
		t.Fatalf("route's app = %d MB, min %d", target.RAMMB, target.MinInstances)
	}
}

// An app with its own edge rules or a static egress IP is refused: the
// route's app would answer without them.
func TestApplyRefusesUncopiableProtections(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.CreateEdgeRule(f.ctx, state.CreateEdgeRuleParams{AccountID: f.acct.ID, AppID: f.source.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/admin/*", Enabled: true, Kind: state.EdgeRuleKindIP, Name: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request()); prob == nil || prob.Code != api.CodeRouteSplitUnsupported {
		t.Fatalf("app-wide edge rules: %+v", prob)
	}
	g := newFixture(t)
	ip := netip.MustParseAddr("198.51.100.7")
	source := g.source
	source.StaticEgressIP = &ip
	if _, prob := g.svc.Apply(g.ctx, g.acct, source, g.request()); prob == nil || prob.Code != api.CodeRouteSplitUnsupported {
		t.Fatalf("static egress IP: %+v", prob)
	}
}

// An app with host rules splits: they still run on routed requests, and the
// route's app gets a deny-all rule on each of its own hosts before any
// traffic moves.
func TestSplitBlocksTheRouteAppsOwnHosts(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.CreateEdgeRule(f.ctx, state.CreateEdgeRuleParams{AccountID: f.acct.ID, AppID: f.source.ID,
		MatchHost: "shop.gregale.dev", MatchPath: "/admin/*", Enabled: true, Kind: state.EdgeRuleKindIP, Name: "admin"}); err != nil {
		t.Fatal(err)
	}
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply with host rules: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.svc.Step(f.ctx) // a retried pass reuses the block rules
	blocks, err := f.store.ListEdgeRulesForApp(f.ctx, split.TargetAppID)
	if err != nil {
		t.Fatal(err)
	}
	hosts := map[string]bool{}
	for _, rule := range blocks {
		if rule.Kind != state.EdgeRuleKindIP || rule.MatchPath != "/**" || rule.Name != routesplit.RuleName(split.ID) {
			t.Fatalf("block rule = %+v", rule)
		}
		hosts[rule.MatchHost] = true
	}
	if len(blocks) != 2 || !hosts["shop-exports.gregale.dev"] || !hosts["shop-exports--*.gregale.dev"] {
		t.Fatalf("block rules = %+v", blocks)
	}
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDone || len(got.EdgeRuleIDs) != 2 {
		t.Fatalf("split = %+v", got)
	}
}

// Apply blocks the route's app's own hosts before it deploys anything; a
// block that cannot be made rolls the split back and leaves nothing behind.
func TestApplyRollsBackWhenTheBlockFails(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if blocks, _ := f.store.ListEdgeRulesForApp(f.ctx, split.TargetAppID); len(blocks) != 2 {
		t.Fatalf("block rules after apply = %+v, want 2 before the deployment is live", blocks)
	}

	g := newFixture(t)
	g.host.ruleErr = api.ErrCapacity("edge rules unavailable")
	if _, prob := g.svc.Apply(g.ctx, g.acct, g.source, g.request()); prob == nil {
		t.Fatal("apply succeeded without its block")
	}
	splits, err := g.store.RouteSplitsForApp(g.ctx, g.acct.ID, g.source.ID, 10)
	if err != nil || len(splits) != 1 || splits[0].Phase != state.RouteSplitRolledBack || !strings.Contains(splits[0].Error, "block direct access") {
		t.Fatalf("splits = %+v, %v", splits, err)
	}
	if len(g.host.deleted) != 1 {
		t.Fatalf("route's app not deleted: %v", g.host.deleted)
	}
	for id := range g.host.deleted {
		if rules, _ := g.store.ListEdgeRulesForApp(g.ctx, id); len(rules) != 0 {
			t.Fatalf("rules left on the deleted route's app: %+v", rules)
		}
	}
}

// The worker repairs a missing block before any route rule, retrying a
// transient failure instead of rolling back; a refusal rolls back.
func TestBlockRepair(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	blocks, _ := f.store.ListEdgeRulesForApp(f.ctx, split.TargetAppID)
	if err := f.store.DeleteEdgeRule(f.ctx, blocks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.host.ruleErr, f.host.ruleErrKind = api.NewProblem(http.StatusServiceUnavailable, "unavailable", "Unavailable", "try again"), "ip"
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDeploying {
		t.Fatalf("after a transient block failure the split is %+v", got)
	}
	if rules, _ := f.store.ListEdgeRulesForApp(f.ctx, f.source.ID); len(rules) != 0 {
		t.Fatalf("route rules before the block is whole: %+v", rules)
	}
	f.host.ruleErr = nil
	f.svc.Step(f.ctx)
	if blocks, _ := f.store.ListEdgeRulesForApp(f.ctx, split.TargetAppID); len(blocks) != 2 {
		t.Fatalf("repaired block rules = %+v", blocks)
	}
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitRuleLive {
		t.Fatalf("after the repair the split is %+v", got)
	}

	g := newFixture(t)
	split, prob = g.svc.Apply(g.ctx, g.acct, g.source, g.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	blocks, _ = g.store.ListEdgeRulesForApp(g.ctx, split.TargetAppID)
	if err := g.store.DeleteEdgeRule(g.ctx, blocks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := g.store.MarkDeploymentLive(g.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	g.host.ruleErr, g.host.ruleErrKind = api.NewProblem(http.StatusPaymentRequired, api.CodePlanEdgeRuleKindNotAllowed, "Not on plan", "ip rules"), "ip"
	g.svc.Step(g.ctx)
	if got := g.read(t, split.ID); got.Phase != state.RouteSplitRolledBack || !strings.Contains(got.Error, "block direct access") {
		t.Fatalf("after a refused block the split is %+v", got)
	}
}

// An account that may not make ip rules is refused before anything is made.
func TestApplyChecksTheIPRuleGate(t *testing.T) {
	f := newFixture(t)
	f.host.kindErr = api.NewProblem(http.StatusPaymentRequired, api.CodePlanEdgeRuleKindNotAllowed, "Not on plan", "ip rules need Hobby")
	_, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob == nil || prob.Code != api.CodePlanEdgeRuleKindNotAllowed || !strings.Contains(prob.Detail, "ip rules need Hobby") {
		t.Fatalf("apply = %+v", prob)
	}
	if splits, _ := f.store.RouteSplitsForApp(f.ctx, f.acct.ID, f.source.ID, 10); len(splits) != 0 {
		t.Fatalf("splits recorded: %+v", splits)
	}
}

// Rules that already decide the split's path, or count it per app, refuse
// the split; ones elsewhere, and earlier splits' rules, do not.
func TestApplyRefusesOverlappingRules(t *testing.T) {
	for _, tc := range []struct {
		name, kind, path, ruleName string
		refused                    bool
	}{
		{"route on the path", "route", "/exports/**", "", true},
		{"rewrite everywhere", "rewrite", "/**", "", true},
		{"redirect elsewhere", "redirect", "/old/*", "", false},
		{"throttle everywhere", "throttle", "/**", "", true},
		{"throttle elsewhere", "throttle", "/api/*", "", false},
		{"earlier split", "route", "/exports/**", routesplit.RuleName("earlier"), false},
		{"ip on the path", "ip", "/exports/*", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.store.CreateEdgeRule(f.ctx, state.CreateEdgeRuleParams{AccountID: f.acct.ID, AppID: f.source.ID,
				MatchHost: "shop.gregale.dev", MatchPath: tc.path, Enabled: true, Kind: state.EdgeRuleKind(tc.kind), Name: tc.ruleName}); err != nil {
				t.Fatal(err)
			}
			_, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
			if refused := prob != nil && prob.Code == api.CodeRouteSplitUnsupported; refused != tc.refused {
				t.Fatalf("refused = %v (%+v), want %v", refused, prob, tc.refused)
			}
		})
	}
}

func TestGlobsMayOverlap(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"/exports/*", "/exports/*", true},
		{"/exports", "/exports/**", true},
		{"/**", "/exports/*", true},
		{"/api/*", "/exports/*", false},
		{"/exports/1", "/exports/*", true},
		{"/export", "/exports/*", false},
		{"/a", "/b", false},
		{"", "/b", true},
	} {
		if got := routesplit.GlobsMayOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("GlobsMayOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// A second undo while the first runs gets 409; after it, the undone split.
func TestConcurrentUndo(t *testing.T) {
	f := newFixture(t)
	f.host.split = &routeresources.Split{Kind: routeresources.SplitCPU, Route: "POST /render", Method: "POST", PathGlob: "/render",
		RouteRAMMB: 1024, CurrentRAMMB: 1024, MainRAMMB: 1024}
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.svc.Step(f.ctx)
	done := f.read(t, split.ID)
	held, ok, err := f.store.ClaimRouteSplit(f.ctx, done, time.Now().Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, prob := f.svc.Undo(f.ctx, f.acct, f.source, done); prob == nil || prob.Status != http.StatusConflict {
		t.Fatalf("undo while another runs: %+v", prob)
	}
	if err := f.store.ReleaseRouteSplit(f.ctx, held); err != nil {
		t.Fatal(err)
	}
	if _, prob := f.svc.Undo(f.ctx, f.acct, f.source, done); prob != nil {
		t.Fatalf("undo: %+v", prob)
	}
	again, prob := f.svc.Undo(f.ctx, f.acct, f.source, f.read(t, split.ID))
	if prob != nil || again.Phase != state.RouteSplitUndone {
		t.Fatalf("repeat undo = %+v, %+v", again, prob)
	}
}

func TestRuleFailureRollsBack(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.host.ruleErr = api.ErrValidation("quota")
	f.svc.Step(f.ctx)
	got := f.read(t, split.ID)
	if got.Phase != state.RouteSplitRolledBack || !f.host.deleted[split.TargetAppID] || len(f.host.resized) != 0 {
		t.Fatalf("split after a rule failure = %+v", got)
	}
}

// A split another pass holds is left alone (two apid replicas).
func TestClaimFencesConcurrentPasses(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	held, ok, err := f.store.ClaimRouteSplit(f.ctx, split, time.Now().Add(time.Minute))
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDeploying {
		t.Fatalf("a held split moved: %+v", got)
	}
	if rules, _ := f.store.ListEdgeRulesForApp(f.ctx, f.source.ID); len(rules) != 0 {
		t.Fatalf("a held split made rules: %+v", rules)
	}
	if err := f.store.ReleaseRouteSplit(f.ctx, held); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitRuleLive {
		t.Fatalf("after release the split is %+v", got)
	}
}

// A transient resize failure is retried on the next pass rather than
// finishing the split with a warning.
func TestResizeRetriesTransientFailures(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.host.resizeErr = api.ErrCapacity("store briefly unavailable")
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitRuleLive {
		t.Fatalf("a transient resize failure finished the split: %+v", got)
	}
	f.host.resizeErr = nil
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDone || !got.MainResized || got.Warning != "" {
		t.Fatalf("retried resize = %+v", got)
	}
}

// Past the retry window the split finishes with a warning, the route moved.
func TestResizeGivesUpAfterWindow(t *testing.T) {
	f := newFixture(t)
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.host.resizeErr = api.ErrCapacity("store down")
	f.clock = f.clock.Add(api.RouteSplitResizeRetryWindow + time.Minute)
	f.svc.Step(f.ctx)
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDone || got.MainResized || got.Warning == "" {
		t.Fatalf("split after the window = %+v", got)
	}
}

// Undo never ends undone while the route's app survives: a 404 from the
// delete counts only when the app is really gone.
func TestUndoKeepsSplitWhenTargetSurvives(t *testing.T) {
	f := newFixture(t)
	f.host.split = &routeresources.Split{Kind: routeresources.SplitCPU, Route: "POST /render", Method: "POST", PathGlob: "/render",
		RouteRAMMB: 1024, CurrentRAMMB: 1024, MainRAMMB: 1024}
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if err := f.store.MarkDeploymentLive(f.ctx, split.TargetDeploymentID); err != nil {
		t.Fatal(err)
	}
	f.svc.Step(f.ctx)
	f.svc.Step(f.ctx)
	done := f.read(t, split.ID)
	if done.Phase != state.RouteSplitDone {
		t.Fatalf("split = %+v", done)
	}
	f.host.deleteNotFound = true
	if _, prob := f.svc.Undo(f.ctx, f.acct, f.source, done); prob == nil {
		t.Fatal("undo succeeded while the route's app survived")
	}
	if got := f.read(t, split.ID); got.Phase != state.RouteSplitDone {
		t.Fatalf("split after a failed undo = %+v", got)
	}
}

// A taken slug gets a suffix instead of failing the split.
func TestApplySuffixesATakenSlug(t *testing.T) {
	f := newFixture(t)
	if _, err := f.store.CreateApp(f.ctx, state.App{AccountID: f.acct.ID, Slug: "shop-exports", Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	split, prob := f.svc.Apply(f.ctx, f.acct, f.source, f.request())
	if prob != nil {
		t.Fatalf("apply: %+v", prob)
	}
	if split.TargetSlug == "shop-exports" || !strings.HasPrefix(split.TargetSlug, "shop-exports-") {
		t.Fatalf("target slug = %q", split.TargetSlug)
	}
	if got := f.read(t, split.ID); got.TargetSlug != split.TargetSlug {
		t.Fatalf("recorded slug %q, returned %q", got.TargetSlug, split.TargetSlug)
	}
}

func TestTargetSlug(t *testing.T) {
	for glob, want := range map[string]string{
		"/exports/*":                             "shop-exports",
		"/v1/exports/*/pdf":                      "shop-pdf",
		"/*":                                     "shop-heavy",
		"/Reports_2024/*":                        "shop-reports2024",
		"/a-very-long-segment-name-that-goes-on": "shop-a-very-long-segment-name-that-goes",
	} {
		if got := routesplit.TargetSlug("shop", glob); got != want {
			t.Errorf("TargetSlug(shop, %s) = %q, want %q", glob, got, want)
		}
	}
}
