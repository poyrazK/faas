package state_test

// PR 1 of the edge-rules rollout (ADR-089, planned). Round-trips
// the edge_rules surface against a real Postgres cluster so a typo
// in the tx body, a missing column scan, or a `match_host` LIKE
// predicate that doesn't honour the `*` glob can't ship silently.
//
// Same pgtest.Open skip-when-no-pg pattern as
// pgstore_alert_rules_test.go. Mirrors MemStore coverage
// (TestMemStoreEdgeRule_* in memstore_edge_rules_test.go) so any
// future drift between the two stores surfaces here at the SQL
// layer, not at first wake in gatewayd.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgSampleEdgeRuleParams is the simplest valid
// CreateEdgeRuleParams the tests below use. Fresh per call so
// callers can mutate fields without aliasing. match_host is
// unique enough per call that the LIKE matcher in
// MatchEdgeRulesForHost doesn't get cross-talk.
func pgSampleEdgeRuleParams(accountID, appID, host string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID:    accountID,
		AppID:        appID,
		MatchHost:    host,
		MatchPath:    "/",
		MatchMethods: []string{},
		Priority:     100,
		Enabled:      true,
		Kind:         state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindRoute,
			Route: &state.EdgeRuleRouteAction{
				TargetAppSlug: "legacy-api",
			},
		},
	}
}

// pgSampleGeoRuleParams mirrors pgSampleEdgeRuleParams for kind=geo
// (ADR-091 D21). The action carries allow + deny ISO 3166-1 alpha-2
// codes that exercise the geo compile path: allow list restricts
// to a specific set, deny list vetoes everything else.
func pgSampleGeoRuleParams(accountID, appID, host string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID:    accountID,
		AppID:        appID,
		MatchHost:    host,
		MatchPath:    "/",
		MatchMethods: []string{"GET", "POST"},
		Priority:     100,
		Enabled:      true,
		Kind:         state.EdgeRuleKindGeo,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindGeo,
			Geo: &state.EdgeRuleGeoAction{
				Allow: []string{"DE", "FR"},
				Deny:  []string{"US"},
			},
		},
	}
}

func pgSampleCacheRuleParams(accountID, appID, host string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID:    accountID,
		AppID:        appID,
		MatchHost:    host,
		MatchPath:    "/products/*",
		MatchMethods: []string{"GET"},
		Priority:     100,
		Enabled:      true,
		Kind:         state.EdgeRuleKindCache,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindCache,
			Cache: &state.EdgeRuleCacheAction{
				MaxAgeSeconds: 30,
				Methods:       []string{"GET"},
			},
		},
	}
}

func TestPgStore_CreateEdgeRuleIfUnderQuota_CacheFreeZeroDenies(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanFree)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanFree, "cache-free-zero")

	_, err := s.CreateEdgeRuleIfUnderQuota(ctx,
		pgSampleCacheRuleParams(acct, app, "cache-free-zero.example.com"), limits)
	if err == nil {
		t.Fatal("Free cache rule was accepted with EdgeRulesCachePerApp=0")
	}
	var quotaErr *state.EdgeRuleQuotaError
	if !errors.As(err, &quotaErr) {
		t.Fatalf("err = %T %v, want *state.EdgeRuleQuotaError", err, err)
	}
	if quotaErr.Limit != 0 || quotaErr.Kind != string(state.EdgeRuleKindCache) || !quotaErr.PerKind {
		t.Fatalf("quota error = %+v, want closed cache quota", quotaErr)
	}
}

// pgEdgeRuleSeedAccount stands up an account + app with a unique
// slug + email so multiple tests in the same schema don't trip the
// (email) UNIQUE or the (slug) UNIQUE on apps.
func pgEdgeRuleSeedAccount(t *testing.T, s *state.PgStore, ctx context.Context, plan api.Plan, suffix string) (acctID, appID string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "edge-rules-"+suffix+"@example.com", plan)
	if err != nil {
		t.Fatalf("CreateAccount(%s): %v", suffix, err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "edge-rules-" + suffix, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp(%s): %v", suffix, err)
	}
	return acct.ID, app.ID
}

// TestPgStore_EdgeRule_RoundTrip exercises Create + GetByID +
// Update + Delete. Proves the SELECT column order in
// scanEdgeRuleCols matches the INSERT statement and that
// delete-then-lookup returns ErrNotFound (no soft-delete residue).
func TestPgStore_EdgeRule_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "roundtrip")

	in := pgSampleEdgeRuleParams(acct, app, "rt.example.com")
	created, err := s.CreateEdgeRule(ctx, in)
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	if created.ID == "" {
		t.Error("CreateEdgeRule returned empty id")
	}
	if created.Kind != state.EdgeRuleKindRoute {
		t.Errorf("kind = %q, want %q", created.Kind, state.EdgeRuleKindRoute)
	}
	if created.Action.Route == nil || created.Action.Route.TargetAppSlug != "legacy-api" {
		t.Errorf("action.route = %+v, want TargetAppSlug=legacy-api", created.Action.Route)
	}

	got, err := s.GetEdgeRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEdgeRuleByID: %v", err)
	}
	if got.MatchHost != "rt.example.com" || got.Priority != 100 {
		t.Errorf("round-trip mismatch: %+v", got)
	}

	// Partial update: priority 100 → 50.
	priority := 50
	updated, err := s.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{
		Priority: &priority,
	})
	if err != nil {
		t.Fatalf("UpdateEdgeRule: %v", err)
	}
	if updated.Priority != 50 {
		t.Errorf("priority after update = %d, want 50", updated.Priority)
	}

	// Action wholesale replace: route → rewrite.
	// The top-level kind column is intentionally NOT touched by
	// UpdateEdgeRule (see pgstore.go:UpdateEdgeRule — kind is
	// immutable across updates because rotating kind mid-life
	// would break the action union). The customer's only path
	// to change kind is delete + recreate. The action JSON's
	// inner `kind` field CAN change (the next two assertions
	// verify that); the top-level `kind` column stays.
	newAction := state.EdgeRuleAction{
		Kind: state.EdgeRuleKindRewrite,
		Rewrite: &state.EdgeRuleRewriteAction{
			From: "/api",
			To:   "/v1",
		},
	}
	updated2, err := s.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{
		Action: &newAction,
	})
	if err != nil {
		t.Fatalf("UpdateEdgeRule action: %v", err)
	}
	if updated2.Kind != state.EdgeRuleKindRoute {
		t.Errorf("kind after action update = %q, want %q (kind is immutable across UpdateEdgeRule)", updated2.Kind, state.EdgeRuleKindRoute)
	}
	if updated2.Action.Rewrite == nil || updated2.Action.Rewrite.From != "/api" {
		t.Errorf("action.rewrite = %+v, want From=/api", updated2.Action.Rewrite)
	}
	if updated2.Action.Route != nil {
		t.Errorf("action.route should be nil after wholesale replace; got %+v", updated2.Action.Route)
	}

	if err := s.DeleteEdgeRule(ctx, created.ID); err != nil {
		t.Fatalf("DeleteEdgeRule: %v", err)
	}
	if _, err := s.GetEdgeRuleByID(ctx, created.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("post-delete lookup = %v; want ErrNotFound", err)
	}
}

// TestPgStore_CreateEdgeRuleIfUnderQuota_PerAppCap fills one app
// to its per-app edge-rules cap (Pro: 100) and asserts the next
// insert returns *state.EdgeRuleQuotaError with the per-app limit
// value. Pins the FOR UPDATE on apps + count predicate in
// pgstore.go. Free plan is 5/app — we use Pro to keep the seed
// loop short and stable across CI shards.
func TestPgStore_CreateEdgeRuleIfUnderQuota_PerAppCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro) // 100/app
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "quota")

	// Seed cap-1 rules so the next insert lands exactly at the cap.
	for i := 0; i < limits.EdgeRulesPerApp-1; i++ {
		in := pgSampleEdgeRuleParams(acct, app, "seed-"+strconv.Itoa(i)+".example.com")
		if _, err := s.CreateEdgeRule(ctx, in); err != nil {
			t.Fatalf("seed #%d: %v", i, err)
		}
	}
	// One more must succeed (under cap).
	if _, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "last.example.com")); err != nil {
		t.Fatalf("at-cap insert: %v", err)
	}

	// One beyond cap must trip *EdgeRuleQuotaError.
	overflow := pgSampleEdgeRuleParams(acct, app, "overflow.example.com")
	_, err := s.CreateEdgeRuleIfUnderQuota(ctx, overflow, limits)
	if err == nil {
		t.Fatal("expected *EdgeRuleQuotaError at per-app cap, got nil")
	}
	var qe *state.EdgeRuleQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *EdgeRuleQuotaError, got %T: %v", err, err)
	}
	if qe.Limit != limits.EdgeRulesPerApp {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.EdgeRulesPerApp)
	}
	if qe.Observed != limits.EdgeRulesPerApp {
		t.Errorf("observed = %d, want %d", qe.Observed, limits.EdgeRulesPerApp)
	}
}

// TestPgStore_EdgeRule_GeoRoundTrip pins the kind=geo storage path
// (ADR-091 D21). The action union's Geo slot round-trips through
// PG with the allow/deny slices preserved. Mirrors the RoundTrip
// test above, but on the kind=geo surface — a regression in
// scanEdgeRuleCols that drops the Geo slot (the column is shared
// with all other kinds via the action JSONB) would surface as a
// nil Geo on the read side.
//
// Created via CreateEdgeRule (not CreateEdgeRuleIfUnderQuota) so
// the test isn't entangled with the per-kind quota test below.
func TestPgStore_EdgeRule_GeoRoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "geo-rt")

	in := pgSampleGeoRuleParams(acct, app, "geo-rt.example.com")
	created, err := s.CreateEdgeRule(ctx, in)
	if err != nil {
		t.Fatalf("CreateEdgeRule(geo): %v", err)
	}
	if created.Kind != state.EdgeRuleKindGeo {
		t.Errorf("kind = %q, want %q", created.Kind, state.EdgeRuleKindGeo)
	}
	if created.Action.Geo == nil {
		t.Fatalf("action.Geo = nil after Create; want populated slice")
	}
	if got := len(created.Action.Geo.Allow); got != 2 {
		t.Errorf("Allow len = %d, want 2", got)
	}
	if got := len(created.Action.Geo.Deny); got != 1 {
		t.Errorf("Deny len = %d, want 1", got)
	}

	got, err := s.GetEdgeRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEdgeRuleByID: %v", err)
	}
	if got.Action.Geo == nil {
		t.Fatalf("action.Geo = nil after Get; want populated slice")
	}
	if got.Action.Geo.Allow[0] != "DE" || got.Action.Geo.Allow[1] != "FR" {
		t.Errorf("Allow = %+v, want [DE, FR]", got.Action.Geo.Allow)
	}
	if got.Action.Geo.Deny[0] != "US" {
		t.Errorf("Deny = %+v, want [US]", got.Action.Geo.Deny)
	}
	if got.Action.Route != nil || got.Action.IP != nil {
		t.Errorf("non-geo action slots leaked: route=%+v ip=%+v", got.Action.Route, got.Action.IP)
	}
}

// TestPgStore_EdgeRule_GeoKindAcceptedByCheckConstraint confirms
// the migration 00207 widening is wired through the runtime: a
// kind='geo' insert succeeds against the live CHECK. The CHECK
// name (`edge_rules_kind_check`) is auto-assigned by PG to the
// inline column constraint, so the migration re-uses the same name.
func TestPgStore_EdgeRule_GeoKindAcceptedByCheckConstraint(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "geo-ck")
	_, err := s.CreateEdgeRule(ctx, pgSampleGeoRuleParams(acct, app, "geo-ck.example.com"))
	if err != nil {
		t.Fatalf("CreateEdgeRule(geo) under widened CHECK: %v", err)
	}
}

// TestPgStore_EdgeRule_NonGeoKindStillAccepted pins that the
// migration 00207 widening did NOT regress the closed set: all 7
// pre-existing kinds still round-trip cleanly.
func TestPgStore_EdgeRule_NonGeoKindStillAccepted(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "non-geo")
	for i, k := range []state.EdgeRuleKind{
		state.EdgeRuleKindRoute,
		state.EdgeRuleKindRewrite,
		state.EdgeRuleKindRedirect,
		state.EdgeRuleKindHeaders,
		state.EdgeRuleKindCORSA,
		state.EdgeRuleKindJWT,
		state.EdgeRuleKindIP,
		state.EdgeRuleKindCache,
	} {
		in := pgSampleEdgeRuleParams(acct, app, "ngeo-"+strconv.Itoa(i)+".example.com")
		in.Kind = k
		in.Action.Kind = k
		if _, err := s.CreateEdgeRule(ctx, in); err != nil {
			t.Errorf("CreateEdgeRule(%s): %v", k, err)
		}
	}
}

// TestPgStore_CreateEdgeRuleIfUnderQuota_GeoPerAppCap fills an app
// to its per-kind geo cap (Pro: EdgeRulesGeoPerApp = 25) and
// asserts the next kind=geo insert returns *state.EdgeRuleQuotaError
// with the per-kind limit. Pins the per-kind gate inside the same
// FOR UPDATE lock as the general cap (ADR-091 D22). We use Pro so
// the seed loop is short and stable across CI shards.
func TestPgStore_CreateEdgeRuleIfUnderQuota_GeoPerAppCap(t *testing.T) {
	s, ctx := pgStore(t)
	limits := api.MustLimitsFor(api.PlanPro)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "geo-quota")

	// Fill to per-kind cap.
	for i := 0; i < limits.EdgeRulesGeoPerApp; i++ {
		in := pgSampleGeoRuleParams(acct, app, "gq-"+strconv.Itoa(i)+".example.com")
		if _, err := s.CreateEdgeRule(ctx, in); err != nil {
			t.Fatalf("seed geo #%d: %v", i, err)
		}
	}

	// One more kind=geo must trip the per-kind quota.
	overflow := pgSampleGeoRuleParams(acct, app, "gq-overflow.example.com")
	_, err := s.CreateEdgeRuleIfUnderQuota(ctx, overflow, limits)
	if err == nil {
		t.Fatal("expected *EdgeRuleQuotaError at per-kind geo cap, got nil")
	}
	var qe *state.EdgeRuleQuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("expected *EdgeRuleQuotaError, got %T: %v", err, err)
	}
	if !qe.PerKind {
		t.Errorf("qe.PerKind = false, want true (per-kind quota path)")
	}
	if qe.Kind != string(state.EdgeRuleKindGeo) {
		t.Errorf("qe.Kind = %q, want %q", qe.Kind, string(state.EdgeRuleKindGeo))
	}
	if qe.Limit != limits.EdgeRulesGeoPerApp {
		t.Errorf("limit = %d, want %d", qe.Limit, limits.EdgeRulesGeoPerApp)
	}
}

// TestPgStore_CountEdgeRulesByKindForApp_GeoScoped pins the new
// store method (ADR-091 D22). Counts of kind=geo must be scoped
// to (app_id, kind) — not bleeding across kinds.
func TestPgStore_CountEdgeRulesByKindForApp_GeoScoped(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "geo-count")

	// 2 route + 3 geo = 5 total.
	for i := 0; i < 2; i++ {
		if _, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "rc-"+strconv.Itoa(i)+".example.com")); err != nil {
			t.Fatalf("seed route #%d: %v", i, err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := s.CreateEdgeRule(ctx, pgSampleGeoRuleParams(acct, app, "gc-"+strconv.Itoa(i)+".example.com")); err != nil {
			t.Fatalf("seed geo #%d: %v", i, err)
		}
	}

	geoCount, err := s.CountEdgeRulesByKindForApp(ctx, app, state.EdgeRuleKindGeo)
	if err != nil {
		t.Fatalf("CountEdgeRulesByKindForApp(geo): %v", err)
	}
	if geoCount != 3 {
		t.Errorf("geo count = %d, want 3", geoCount)
	}

	routeCount, err := s.CountEdgeRulesByKindForApp(ctx, app, state.EdgeRuleKindRoute)
	if err != nil {
		t.Fatalf("CountEdgeRulesByKindForApp(route): %v", err)
	}
	if routeCount != 2 {
		t.Errorf("route count = %d, want 2", routeCount)
	}
}

// TestPgStore_EdgeRule_ListForAccountScopesByAccount pins the
// dashboard hydrate path: rules under account A must not leak
// into account B's listing, and CountEdgeRulesForApp matches the
// list size.
func TestPgStore_EdgeRule_ListForAccountScopesByAccount(t *testing.T) {
	s, ctx := pgStore(t)
	acctA, appA := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "listA")
	acctB, appB := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "listB")

	// Three rules on A, one on B.
	for i := 0; i < 3; i++ {
		if _, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acctA, appA, "a-"+strconv.Itoa(i)+".example.com")); err != nil {
			t.Fatalf("seed A #%d: %v", i, err)
		}
	}
	if _, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acctB, appB, "b-0.example.com")); err != nil {
		t.Fatalf("seed B: %v", err)
	}

	listA, err := s.ListEdgeRulesForAccount(ctx, acctA)
	if err != nil {
		t.Fatalf("ListA: %v", err)
	}
	if len(listA) != 3 {
		t.Errorf("ListA = %d, want 3", len(listA))
	}
	for _, r := range listA {
		if r.AccountID != acctA {
			t.Errorf("ListA leaked rule from account %q", r.AccountID)
		}
	}

	listB, err := s.ListEdgeRulesForAccount(ctx, acctB)
	if err != nil {
		t.Fatalf("ListB: %v", err)
	}
	if len(listB) != 1 {
		t.Errorf("ListB = %d, want 1", len(listB))
	}

	// ListEdgeRulesForApp agrees with CountEdgeRulesForApp.
	listAppA, err := s.ListEdgeRulesForApp(ctx, appA)
	if err != nil {
		t.Fatalf("ListAppA: %v", err)
	}
	count, err := s.CountEdgeRulesForApp(ctx, appA)
	if err != nil {
		t.Fatalf("CountEdgeRulesForApp: %v", err)
	}
	if count != len(listAppA) {
		t.Errorf("count=%d vs list=%d (drift between Count and List)", count, len(listAppA))
	}
}

// TestPgStore_EdgeRule_MatchForHost_Glob pins the gateway hot-path
// read: `match_host = '*'` is wildcard; exact matches win over
// glob matches; `*.example.com` matches any subdomain but not the
// bare apex. The LIKE rewrite must NOT match unrelated hosts.
func TestPgStore_EdgeRule_MatchForHost_Glob(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "match")

	// Three rules:
	//  1. wildcard (match_host='*')
	//  2. exact (api.example.com)
	//  3. subdomain glob (*.example.com)
	for _, in := range []state.CreateEdgeRuleParams{
		pgSampleEdgeRuleParams(acct, app, "*"),
		pgSampleEdgeRuleParams(acct, app, "api.example.com"),
		pgSampleEdgeRuleParams(acct, app, "*.example.com"),
	} {
		if _, err := s.CreateEdgeRule(ctx, in); err != nil {
			t.Fatalf("seed %q: %v", in.MatchHost, err)
		}
	}

	// 1. Subdomain lookup: must match wildcard + subdomain glob (2 rules).
	got, err := s.MatchEdgeRulesForHost(ctx, "blog.example.com")
	if err != nil {
		t.Fatalf("Match subdomain: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("subdomain lookup = %d rules, want 2 (wildcard + *.example.com); got %+v", len(got), got)
	}

	// 2. Exact host: must match wildcard + exact + subdomain glob (3 rules).
	got, err = s.MatchEdgeRulesForHost(ctx, "api.example.com")
	if err != nil {
		t.Fatalf("Match exact: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("exact lookup = %d rules, want 3 (wildcard + exact + *.example.com)", len(got))
	}

	// 3. Apex (no subdomain): must NOT match *.example.com — the
	// glob rewrite must require a leading subdomain segment.
	got, err = s.MatchEdgeRulesForHost(ctx, "example.com")
	if err != nil {
		t.Fatalf("Match apex: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("apex lookup = %d rules, want 1 (wildcard only); got %+v", len(got), got)
	}

	// 4. Unrelated host: must match wildcard only.
	got, err = s.MatchEdgeRulesForHost(ctx, "other.com")
	if err != nil {
		t.Fatalf("Match unrelated: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("unrelated lookup = %d rules, want 1 (wildcard only); got %+v", len(got), got)
	}
}

// TestPgStore_EdgeRule_DisabledRulesExcludedFromMatch pins the
// partial-index discipline: disabled rules must NOT land in
// MatchEdgeRulesForHost (gateway must never see a disabled rule).
// Same constraint as alert_rules (TestPgStore_AlertRule_ListAndFilter).
func TestPgStore_EdgeRule_DisabledRulesExcludedFromMatch(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "disabled")

	enabled, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "en.example.com"))
	if err != nil {
		t.Fatalf("seed enabled: %v", err)
	}
	disabled, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "dis.example.com"))
	if err != nil {
		t.Fatalf("seed disabled: %v", err)
	}
	enabledFalse := false
	if _, err := s.UpdateEdgeRule(ctx, disabled.ID, state.UpdateEdgeRuleParams{Enabled: &enabledFalse}); err != nil {
		t.Fatalf("flip to disabled: %v", err)
	}

	got, err := s.MatchEdgeRulesForHost(ctx, "en.example.com")
	if err != nil {
		t.Fatalf("Match enabled host: %v", err)
	}
	if len(got) != 1 || got[0].ID != enabled.ID {
		t.Errorf("Match enabled host = %+v, want only the enabled rule", got)
	}

	got, err = s.MatchEdgeRulesForHost(ctx, "dis.example.com")
	if err != nil {
		t.Fatalf("Match disabled host: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Match disabled host = %+v, want empty (disabled rule leaked)", got)
	}
}

// TestPgStore_EdgeRule_DeleteUnknownReturnsErrNotFound guards the
// not-found branch — callers must not silently no-op when a stale
// id is passed.
func TestPgStore_EdgeRule_DeleteUnknownReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	if err := s.DeleteEdgeRule(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteEdgeRule(unknown) = %v, want ErrNotFound", err)
	}
}

// TestPgStore_EdgeRule_UpdatedAtBumpsOnUpdate pins the BEFORE
// UPDATE trigger: every Update must move updated_at forward so
// gatewayd's pg_notify listener can detect the change. Without
// the trigger, an LRU that uses updated_at as the cache key would
// never invalidate.
func TestPgStore_EdgeRule_UpdatedAtBumpsOnUpdate(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "upd")
	created, err := s.CreateEdgeRule(ctx, pgSampleEdgeRuleParams(acct, app, "bump.example.com"))
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}

	// Sleep so the trigger's now() is strictly greater than the
	// create-time default. 10ms is comfortable under CI load.
	time.Sleep(10 * time.Millisecond)

	enabled := false
	updated, err := s.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdateEdgeRule: %v", err)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at didn't advance: before=%v after=%v", created.UpdatedAt, updated.UpdatedAt)
	}
}

// pgSampleValidateRuleParams is the simplest valid
// CreateEdgeRuleParams for kind=validate. The validate_mode
// column is the load-bearing surface for ADR-128 §D1; the
// companion tests below pin its round-trip + Update semantics.
func pgSampleValidateRuleParams(accountID, appID, host, mode string) state.CreateEdgeRuleParams {
	return state.CreateEdgeRuleParams{
		AccountID:    accountID,
		AppID:        appID,
		MatchHost:    host,
		MatchPath:    "/",
		MatchMethods: []string{"POST"},
		Priority:     100,
		Enabled:      true,
		Kind:         state.EdgeRuleKindValidate,
		ValidateMode: mode,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindValidate,
			Validate: &state.EdgeRuleValidateAction{
				Schema: []byte(`{"type":"object","required":["x"]}`),
			},
		},
	}
}

// TestPgStore_EdgeRule_ValidateModeTopLevelRoundTrip pins the
// top-level validate_mode column (ADR-128 §D1). Create with
// 'warn' → GetByID returns 'warn' → Update to 'observe' → GetByID
// returns 'observe' → Update with empty string → SQL coalesce
// keeps the existing 'observe' (the empty path is "leave alone").
// A regression in the column-list scan or the INSERT/UPDATE
// param binding surfaces as a wrong value here.
func TestPgStore_EdgeRule_ValidateModeTopLevelRoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "vt-rt")

	created, err := s.CreateEdgeRule(ctx, pgSampleValidateRuleParams(acct, app, "vmodert.example.com", "warn"))
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	if created.ValidateMode != "warn" {
		t.Errorf("after create ValidateMode = %q, want %q (top-level column scan must round-trip)", created.ValidateMode, "warn")
	}

	got, err := s.GetEdgeRuleByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetEdgeRuleByID: %v", err)
	}
	if got.ValidateMode != "warn" {
		t.Errorf("GetByID ValidateMode = %q, want %q", got.ValidateMode, "warn")
	}

	// Update: warn → observe. The store coalesces the empty
	// path to "leave alone", so passing a non-empty value is
	// the explicit-update path.
	observe := "observe"
	updated, err := s.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{ValidateMode: &observe})
	if err != nil {
		t.Fatalf("UpdateEdgeRule: %v", err)
	}
	if updated.ValidateMode != "observe" {
		t.Errorf("after Update ValidateMode = %q, want %q", updated.ValidateMode, "observe")
	}

	// Update with empty string: pgstore coalesces '' to
	// validate_mode (leave alone). The previous 'observe'
	// value sticks.
	empty := ""
	updated2, err := s.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{ValidateMode: &empty})
	if err != nil {
		t.Fatalf("UpdateEdgeRule (empty): %v", err)
	}
	if updated2.ValidateMode != "observe" {
		t.Errorf("after empty-string Update ValidateMode = %q, want %q (empty must coalesce to leave-alone)", updated2.ValidateMode, "observe")
	}

	// Update with nil pointer: leave-alone (no column write).
	// The previous value still wins on the read-back.
	updated3, err := s.UpdateEdgeRule(ctx, created.ID, state.UpdateEdgeRuleParams{Priority: ptr(75)})
	if err != nil {
		t.Fatalf("UpdateEdgeRule (no-mode): %v", err)
	}
	if updated3.ValidateMode != "observe" {
		t.Errorf("after no-mode Update ValidateMode = %q, want %q (nil pointer must leave alone)", updated3.ValidateMode, "observe")
	}
	if updated3.Priority != 75 {
		t.Errorf("priority after no-mode Update = %d, want 75", updated3.Priority)
	}
}

// TestPgStore_EdgeRule_ValidateModeDefaultBlock pins the
// NOT NULL DEFAULT 'block' behaviour: an empty ValidateMode on
// Create coalesces to 'block' at the SQL boundary (the wire
// handler relies on this for the create path; legacy rows
// pre-00293 also land here).
func TestPgStore_EdgeRule_ValidateModeDefaultBlock(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "vt-block")

	created, err := s.CreateEdgeRule(ctx, pgSampleValidateRuleParams(acct, app, "vmodeblk.example.com", ""))
	if err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	if created.ValidateMode != "block" {
		t.Errorf("empty ValidateMode coerced to %q, want %q (NOT NULL DEFAULT 'block')", created.ValidateMode, "block")
	}
}

// TestPgStore_EdgeRule_ValidateModeInvalidRejected pins the
// CHECK constraint on validate_mode. A non-closed value must
// surface as SQLSTATE 23514 (check_violation) so the apid
// handler can map it to a 422 — silently coercing to 'block'
// would mask a customer typo.
func TestPgStore_EdgeRule_ValidateModeInvalidRejected(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "vt-inv")

	_, err := s.CreateEdgeRule(ctx, pgSampleValidateRuleParams(acct, app, "vmodeinv.example.com", "yolo"))
	if err == nil {
		t.Fatal("CreateEdgeRule accepted validate_mode='yolo'; want CHECK constraint rejection")
	}
	// The pgx error wraps the SQLSTATE 23514 in the message;
	// a substring match is enough — the apid handler's
	// EdgeRuleQuotaError / Conflict mapping uses the same shape.
	if !strings.Contains(err.Error(), "validate_mode") &&
		!strings.Contains(err.Error(), "check") {
		t.Errorf("rejection error = %v; want one mentioning validate_mode / check constraint", err)
	}
}

func ptr[T any](v T) *T { return &v }
