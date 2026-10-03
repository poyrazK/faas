//go:build !no_pg

// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func trafficHostPGFixture(t *testing.T) (*PgStore, *pgxpool.Pool, Account, App) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), "host-analysis@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "host-analysis", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return store, pool, account, app
}

func trafficHostRule(account Account, app App, host string) CreateEdgeRuleParams {
	return CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: host, MatchPath: "/", Enabled: true,
		Kind: EdgeRuleKindRoute, Action: EdgeRuleAction{Kind: EdgeRuleKindRoute, Route: &EdgeRuleRouteAction{TargetAppSlug: app.Slug}}}
}

func TestPgTrafficHostSelectorMatchesPostgres(t *testing.T) {
	_, pool, _, _ := trafficHostPGFixture(t)
	patterns := []string{"*", "api.*", "*.test", "?pi.test", "_pi.test", "api.%", `api.\%`, `api.\?`, `api.\\*`, "é?.test", "a*?*b"}
	hosts := []string{"", "api.test", "web.test", "api.", "épi.test", "api.%", `api.\%`, `api.\?`, "api._", `api.\suffix`, "éa.test", "ab", "acb", "axxxb"}
	machine := hostAnalysisMachine{nodes: []hostAnalysisNode{newHostAnalysisNode()}, maxNodes: api.TrafficPolicyMaxAnalysisNodes}
	for index, pattern := range patterns {
		if err := machine.add(pattern, hostAnalysisRef{group: index}); err != nil {
			t.Fatal(err)
		}
	}
	for _, host := range hosts {
		positions := machine.closure([]int{0})
		for _, character := range host {
			positions = machine.step(positions, character)
		}
		accepted := make(map[int]bool)
		for _, position := range positions {
			for _, ref := range machine.nodes[position].accepted {
				accepted[ref.group] = true
			}
		}
		for index, pattern := range patterns {
			var postgres bool
			if err := pool.QueryRow(t.Context(), `SELECT $1::text=$2::text OR $2::text='*' OR $1::text LIKE replace(replace($2::text,'*','%'),'?','_')`, host, pattern).Scan(&postgres); err != nil {
				t.Fatal(err)
			}
			if accepted[index] != postgres {
				t.Fatalf("selector %q host %q: analysis=%v postgres=%v", pattern, host, accepted[index], postgres)
			}
		}
	}
}

func TestPgTrafficHostEstimateBoundsDecodedRuntime(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	preset, err := store.CreateCorsPresetIfUnderQuota(t.Context(), CorsPreset{AccountID: account.ID, Name: "escaped",
		AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, AllowHeaders: []string{"<>&\u2028\u2029"}}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	actions := []string{`{}`, `{"cors":{}}`,
		`{"headers":{"request_headers":[{},null,{"name":"<>&\u2028\u2029","action":null}]}}`,
		`{"cors":{"allow_credentials":null},"route":{},"jwt":{},"retry":{},"circuit_breaker":{},"validate":{},"respond":{},"budget":{},"limit":{},"throttle":{},"async":{}}`,
		fmt.Sprintf(`{"validate":{"schema":{%q:[1e130000]}}}`, strings.Repeat("<>&\u2028\u2029", 200)),
		`{"validate":{"schema":{"plain":[1e130000]}}}`,
		fmt.Sprintf(`{"validate":{"schema":{"plain":[1e130000],"text":%q}}}`, strings.Repeat("<>&\u2028\u2029", 200)),
	}
	for index, action := range actions {
		host := fmt.Sprintf("shape-%d.example.test", index)
		rule, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, app, host))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE edge_rules SET kind='cors', action=$2::jsonb, cors_preset_id=$3 WHERE id=$1`, rule.ID, action, preset.ID); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := sqlc.New().RestoreTrafficPolicyStatementTimeout(t.Context(), tx, "5s"); err != nil {
		t.Fatal(err)
	}
	var analysis trafficHostAnalysis
	for _, jit := range []string{"on", "off"} {
		var setting string
		if err := tx.QueryRow(t.Context(), `SELECT set_config('jit',$1,true)`, jit).Scan(&setting); err != nil {
			t.Fatal(err)
		}
		configured, err := sqlc.New().ConfigureTrafficPolicyAnalysisTimeout(t.Context(), tx,
			fmt.Sprintf("%dms", api.TrafficPolicyAnalysisSQLTimeout.Milliseconds()))
		if err != nil || configured.Prior != "5s" || configured.PriorJit != jit {
			t.Fatalf("analysis lost prior settings: configured=%+v err=%v", configured, err)
		}
		if err := tx.QueryRow(t.Context(), `SHOW jit`).Scan(&setting); err != nil || setting != "off" {
			t.Fatalf("bounded analysis retained JIT compilation: setting=%q err=%v", setting, err)
		}
		if _, err := sqlc.New().RestoreTrafficPolicyAnalysisSettings(t.Context(), tx,
			sqlc.RestoreTrafficPolicyAnalysisSettingsParams{Timeout: configured.Prior, Jit: configured.PriorJit}); err != nil {
			t.Fatal(err)
		}
		analysis, err = readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), store.trafficAppsSuffix)
		if err != nil {
			t.Fatal(err)
		}
		if err := tx.QueryRow(t.Context(), `SHOW statement_timeout`).Scan(&setting); err != nil || setting != "5s" {
			t.Fatalf("analysis did not restore statement timeout: setting=%q err=%v", setting, err)
		}
		if err := tx.QueryRow(t.Context(), `SHOW jit`).Scan(&setting); err != nil || setting != jit {
			t.Fatalf("analysis did not restore JIT: setting=%q want=%q err=%v", setting, jit, err)
		}
	}
	if len(analysis.Groups) != len(actions) || len(analysis.Assets) != 1 {
		t.Fatalf("unexpected skinny analysis: %+v", analysis)
	}
	reader := newPublicHostPolicyReader(tx)
	for index, group := range analysis.Groups {
		rules, err := reader.PublicHostEdgeRules(t.Context(), group.Pattern, account.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(rules)
		if err != nil {
			t.Fatal(err)
		}
		observed := len(encoded)
		if group.Preset != "" {
			pinned, err := reader.GetCorsPresetByID(t.Context(), account.ID, group.Preset)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(pinned)
			if err != nil {
				t.Fatal(err)
			}
			observed += len(encoded)
		}
		estimate := hostTotals(analysis, map[int]bool{index: true})
		if estimate.compiled < int64(observed) {
			t.Fatalf("compiled estimate undercounts %s: estimate=%d actual=%d", group.Pattern, estimate.compiled, observed)
		}
		row, err := sqlc.New().ReadPublicHostEdgeRules(t.Context(), tx, sqlc.ReadPublicHostEdgeRulesParams{
			Host: group.Pattern, AccountID: uuidToPgtype(account.ID), MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
		if err != nil || estimate.canonical != int64(len(row.Data))+2 {
			t.Fatalf("canonical bound differs: estimate=%d actual=%d err=%v", estimate.canonical, len(row.Data), err)
		}
	}
	defaults, err := trafficHostActionDefaults()
	if err != nil {
		t.Fatal(err)
	}
	for _, bounds := range []sqlc.ReadTrafficHostAnalysisParams{
		{AccountID: uuidToPgtype(account.ID), Defaults: defaults, MaxInputs: 1, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes},
		{AccountID: uuidToPgtype(account.ID), Defaults: defaults, MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: 1},
	} {
		row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), tx, bounds)
		if err != nil || len(row.Data) != 0 || row.Inputs <= int64(bounds.MaxInputs) && row.Bytes <= bounds.MaxBytes {
			t.Fatalf("oversized metadata transferred: %+v err=%v", row, err)
		}
	}
}

func TestPgTrafficHostConcurrentReferencesAndPresetRollback(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peer, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "host-peer", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a near-bound legacy graph directly, then verify management mutations
	// against the real store. Ampersands exercise compiler bytes independently
	// of the smaller canonical SQL projection and per-preset body cap.
	if _, err := pool.Exec(t.Context(), `INSERT INTO cors_presets(account_id,name,allow_origins,allow_methods,allow_headers,expose_headers)
		SELECT $1, 'aggregate-'||g, ARRAY['*'], ARRAY['GET'], ARRAY[repeat('&',80000)], ARRAY[]::text[] FROM generate_series(1,140) g`, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action,cors_preset_id)
		SELECT $1,$2,'aggregate.example.test','/',true,'cors','{"kind":"cors","cors":{}}',id FROM cors_presets WHERE name NOT IN ('aggregate-139','aggregate-140')`, account.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	presets, err := store.ListCorsPresetsForAccount(t.Context(), account.ID)
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]CorsPreset)
	for _, preset := range presets {
		byName[preset.Name] = preset
	}
	before, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for index, candidate := range []App{app, peer} {
		in := trafficHostRule(account, candidate, "aggregate.example.test")
		presetID := byName[fmt.Sprintf("aggregate-%d", 139+index)].ID
		in.Kind, in.CorsPresetID = EdgeRuleKindCORSA, &presetID
		in.Action = EdgeRuleAction{Kind: EdgeRuleKindCORSA, CORS: &EdgeRuleCORSAction{CorsPresetID: &presetID}}
		go func() {
			<-start
			_, err := store.CreateEdgeRuleIfUnderQuota(t.Context(), in, api.MustLimitsFor(account.Plan))
			results <- err
		}()
	}
	close(start)
	accepted, refused := 0, 0
	for range 2 {
		err := <-results
		var aggregate *TrafficPolicyAggregateError
		switch {
		case err == nil:
			accepted++
		case errors.As(err, &aggregate) && aggregate.Scope == "host_compiled_projection_estimate":
			refused++
		default:
			t.Fatalf("unexpected last-host-budget result: %v", err)
		}
	}
	after, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil || accepted != 1 || refused != 1 || after != before+1 {
		t.Fatalf("aggregate race/change rollback: accepted=%d refused=%d before=%d after=%d err=%v", accepted, refused, before, after, err)
	}
	preset := byName["aggregate-1"]
	beforePreset, err := store.LatestCorsPresetChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	preset.AllowHeaders = []string{strings.Repeat("&", 150000)}
	_, err = store.UpdateCorsPreset(t.Context(), account.ID, preset.ID, preset)
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_compiled_projection_estimate" {
		t.Fatalf("preset growth did not refuse: %v", err)
	}
	afterPreset, err := store.LatestCorsPresetChangeID(t.Context())
	if err != nil || afterPreset != beforePreset {
		t.Fatalf("refused preset emitted change: before=%d after=%d err=%v", beforePreset, afterPreset, err)
	}
	saved, err := store.GetCorsPresetByID(t.Context(), account.ID, preset.ID)
	if err != nil || len(saved.AllowHeaders[0]) != 80000 {
		t.Fatalf("refused preset changed intent: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE cors_presets SET allow_headers=ARRAY[repeat('&',200000)] WHERE id=$1`, preset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateCorsPreset(t.Context(), account.ID, preset.ID, preset); err != nil {
		t.Fatalf("incremental oversized-host repair: %v", err)
	}
	if _, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, peer, "independent.example.test")); err != nil {
		t.Fatalf("unrelated host blocked by legacy overload: %v", err)
	}
}

func TestPgTrafficHostCanonicalOverlapAndDisjointWrites(t *testing.T) {
	store, _, account, app := trafficHostPGFixture(t)
	apiHost, webHost := "api.example.test", "web.example.test"
	in := trafficHostRule(account, app, apiHost)
	in.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 259) + "1e130000]")}
	first, err := store.CreateEdgeRule(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	in.MatchHost = webHost
	second, err := store.CreateEdgeRuleIfUnderQuota(t.Context(), in, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatalf("disjoint hosts became an account quota: %v", err)
	}
	before, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	in.MatchHost = "*"
	_, err = store.CreateEdgeRule(t.Context(), in)
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" {
		t.Fatalf("overlapping numeric expansion accepted: %v", err)
	}
	_, err = store.UpdateEdgeRule(t.Context(), second.ID, UpdateEdgeRuleParams{MatchHost: &apiHost})
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" {
		t.Fatalf("retargeted aggregate accepted: %v", err)
	}
	after, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil || after != before {
		t.Fatalf("refused rules emitted changes: before=%d after=%d err=%v", before, after, err)
	}
	saved, err := store.GetEdgeRuleByID(t.Context(), second.ID)
	if err != nil || saved.MatchHost != webHost || !saved.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("retarget refusal changed intent: %v", err)
	}
	enabled := false
	if _, err := store.UpdateEdgeRule(t.Context(), first.ID, UpdateEdgeRuleParams{Enabled: &enabled}); err != nil {
		t.Fatalf("disable recovery: %v", err)
	}
	if _, err := store.UpdateEdgeRule(t.Context(), second.ID, UpdateEdgeRuleParams{MatchHost: &apiHost}); err != nil {
		t.Fatalf("repaired retarget: %v", err)
	}
}

func TestPgTrafficHostAnalysisTimeoutRefusesAndReleasesLock(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	lock, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Rollback(context.Background()) }()
	if _, err := lock.Exec(t.Context(), `LOCK TABLE edge_rules IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, err = store.CreateEdgeRule(ctx, trafficHostRule(account, app, "timeout.example.test"))
	var analysis *TrafficPolicyAnalysisError
	if !errors.As(err, &analysis) || analysis.Scope != "database_time" || analysis.Unit != "milliseconds" || analysis.Observed <= analysis.Limit || ctx.Err() != nil {
		t.Fatalf("blocked analysis did not refuse independently of caller: %v ctx=%v", err, ctx.Err())
	}
	if err := lock.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `SELECT id FROM accounts WHERE id=$1 FOR UPDATE NOWAIT`, account.ID); err != nil {
		t.Fatalf("refused analysis retained account lock: %v", err)
	}
	if count, err := store.CountEdgeRulesForApp(t.Context(), app.ID); err != nil || count != 0 {
		t.Fatalf("timed out analysis changed intent: count=%d err=%v", count, err)
	}
	if _, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, app, "timeout.example.test")); err != nil {
		t.Fatalf("retry after repaired store: %v", err)
	}
}

func TestPgTrafficHostNoncanonicalLegacyShapeRefusesAndRepairs(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	other, err := store.CreateAccount(t.Context(), "independent-host-analysis@example.test", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := store.CreateApp(t.Context(), App{AccountID: other.ID, Slug: "independent-host-analysis", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	for index, action := range []string{`{"CORS":{}}`, `{"cors":{"Cors_Preset_Id":null}}`, `{"headers":{"REQUEST_HEADERS":[null]}}`} {
		rule, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, app, fmt.Sprintf("legacy-%d.example.test", index)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE edge_rules SET action=$2::jsonb WHERE id=$1`, rule.ID, action); err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateEdgeRule(t.Context(), trafficHostRule(account, app, "unverified.example.test"))
		var analysis *TrafficPolicyAnalysisError
		if !errors.As(err, &analysis) || analysis.Scope != "stored_action_shape" {
			t.Fatalf("unverified decoded shape accepted: %v", err)
		}
		independent := trafficHostRule(other, peer, fmt.Sprintf("independent-%d.example.test", index))
		_, err = store.CreateEdgeRule(t.Context(), independent)
		if !errors.As(err, &analysis) || analysis.Scope != "global_route_stored_action_shape" {
			t.Fatalf("unverified global route shape accepted: %v", err)
		}
		independent.Kind = EdgeRuleKindHeaders
		independent.Action = EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{}}
		if _, err := store.CreateEdgeRule(t.Context(), independent); err != nil {
			t.Fatalf("another account's legacy route shape blocked an owned non-route host: %v", err)
		}
		if _, err := store.UpdateEdgeRule(t.Context(), rule.ID, UpdateEdgeRuleParams{Action: &rule.Action}); err != nil {
			t.Fatalf("supported replacement repair: %v", err)
		}
	}
	if _, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, app, "verified.example.test")); err != nil {
		t.Fatalf("repaired legacy shape still refuses: %v", err)
	}
}
