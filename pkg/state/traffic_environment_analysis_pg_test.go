//go:build !no_pg

// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func trafficEnvironmentPGFixture(t *testing.T) (*PgStore, *pgxpool.Pool, Account, Project, App, ProjectEnvironment) {
	t.Helper()
	store, pool, account, _ := trafficHostPGFixture(t)
	project, err := store.CreateProject(t.Context(), Project{AccountID: account.ID, Slug: "environment-analysis", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "web", Slug: "environment-web", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.ProjectEnvironmentBySlug(t.Context(), account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	return store, pool, account, project, app, environment
}

func trafficEnvironmentHeaders(size int) []ProjectEnvironmentEdgeRule {
	rules := make([]ProjectEnvironmentEdgeRule, 20)
	for index := range rules {
		rules[index] = ProjectEnvironmentEdgeRule{Kind: EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
			Action: EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{
				ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Projection", Action: "set", Value: strings.Repeat("&", size)}},
			}}}
	}
	return rules
}

func TestPgTrafficEnvironmentEstimateMatchesRuntimeReplacement(t *testing.T) {
	store, pool, account, project, app, environment := trafficEnvironmentPGFixture(t)
	host := hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, environment.ID, app.ID)
	peer, err := store.CreateApp(t.Context(), App{AccountID: account.ID, ProjectID: project.ID, WorkloadName: "peer", Slug: "environment-peer", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	preset, err := store.CreateCorsPresetIfUnderQuota(t.Context(), CorsPreset{AccountID: account.ID, Name: "environment-cors", AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, AllowHeaders: []string{"<>&"}}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []App{app, peer} {
		if _, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, owner, host)); err != nil {
			t.Fatal(err)
		}
	}
	in := trafficHostRule(account, app, host)
	in.Kind, in.CorsPresetID = EdgeRuleKindCORSA, &preset.ID
	in.Action = EdgeRuleAction{Kind: EdgeRuleKindCORSA, CORS: &EdgeRuleCORSAction{CorsPresetID: &preset.ID}}
	if _, err := store.CreateEdgeRule(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	in.Kind, in.CorsPresetID = EdgeRuleKindHeaders, nil
	in.Action = EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Projection", Action: "set", Value: "private-analysis-marker<>&"}}}}
	if _, err := store.CreateEdgeRule(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		rules []ProjectEnvironmentEdgeRule
		count int64
	}{
		{"fallback", nil, 3},
		{"replacement", trafficEnvironmentHeaders(4)[:1], 2},
		{"explicit-empty", []ProjectEnvironmentEdgeRule{}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.rules != nil {
				_, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: environment.Slug, Rules: test.rules})
				if err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID))
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(map[int]bool)
			for index, group := range view.Groups {
				if group.Pattern == host {
					accepted[index] = true
				}
			}
			var binding *trafficHostEnvironment
			for index := range view.Environments {
				if view.Environments[index].Host == host {
					binding = &view.Environments[index]
				}
			}
			if binding == nil {
				t.Fatal("registered environment URL missing from analysis")
			}
			reader := newPublicHostPolicyReader(tx)
			originals, err := reader.PublicHostEdgeRules(t.Context(), host, account.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			var overlay *ProjectEnvironmentEdgePolicy
			policy, err := reader.GetProjectEnvironmentEdgePolicy(t.Context(), account.ID, app.ID, environment.Slug)
			if err == nil {
				overlay = &policy
			} else if !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			compiled := ProjectEnvironmentPolicyRules(host, environment, app, originals, overlay)
			data, err := json.Marshal(compiled)
			if err != nil {
				t.Fatal(err)
			}
			actualBytes := len(data)
			seen := make(map[string]bool)
			for _, rule := range compiled {
				if rule.Kind == EdgeRuleKindCORSA && rule.Action.CORS != nil && rule.Action.CORS.CorsPresetID != nil && !seen[*rule.Action.CORS.CorsPresetID] {
					id := *rule.Action.CORS.CorsPresetID
					seen[id] = true
					asset, err := reader.GetCorsPresetByID(t.Context(), account.ID, id)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(asset)
					if err != nil {
						t.Fatal(err)
					}
					actualBytes += len(encoded)
				}
			}
			estimate := environmentHostTotals(view, accepted, binding)
			if estimate.rows != 4 || estimate.compiledRows != test.count || int64(len(compiled)) != test.count || estimate.compiled < int64(actualBytes) {
				t.Fatalf("runtime estimate disagrees: estimate=%+v runtime rows=%d bytes=%d", estimate, len(compiled), actualBytes)
			}
			canonical, err := sqlc.New().ReadPublicHostEdgeRules(t.Context(), tx, sqlc.ReadPublicHostEdgeRulesParams{Host: host, AccountID: uuidToPgtype(account.ID), MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
			if err != nil || estimate.canonical != int64(len(canonical.Data))+2 {
				t.Fatalf("original account read bound: estimate=%+v err=%v", estimate, err)
			}
			if overlay != nil {
				contract, err := sqlc.New().ReadPublicHostEnvironmentPolicy(t.Context(), tx, sqlc.ReadPublicHostEnvironmentPolicyParams{AccountID: uuidToPgtype(account.ID), AppID: uuidToPgtype(app.ID), Scope: environment.Slug, MaxBytes: api.TrafficPolicyMaxContractBytes})
				if err != nil || binding.ContractBytes != int64(len(contract.Data)) {
					t.Fatalf("overlay contract size: metadata=%d err=%v", binding.ContractBytes, err)
				}
			}
			defaults, err := trafficHostActionDefaults()
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), tx, sqlc.ReadTrafficHostAnalysisParams{
				AccountID: uuidToPgtype(account.ID), Defaults: defaults, EnvironmentHostBytes: int32(len(host)),
				MaxInputs: api.TrafficPolicyMaxAnalysisInputs, MaxBytes: api.TrafficPolicyMaxAnalysisMetadataBytes})
			if err != nil || strings.Contains(string(metadata.Data), "private-analysis-marker") {
				t.Fatalf("analysis transferred action body: %v", err)
			}
		})
	}
}

func TestPgTrafficEnvironmentConcurrentOverlayAndRuleRollback(t *testing.T) {
	store, pool, account, project, app, _ := trafficEnvironmentPGFixture(t)
	base := trafficHostRule(account, app, "*")
	base.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 507) + "1e130000]")}
	if _, err := store.CreateEdgeRule(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	before, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	policy := ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production", Rules: trafficEnvironmentHeaders(8000)}
	extra := trafficHostRule(account, app, "*")
	extra.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 3) + "1e130000]")}
	type verdict struct {
		kind string
		err  error
	}
	start, results := make(chan struct{}), make(chan verdict, 2)
	go func() {
		<-start
		_, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), policy)
		results <- verdict{"overlay", err}
	}()
	go func() {
		<-start
		_, err := store.CreateEdgeRuleIfUnderQuota(t.Context(), extra, api.MustLimitsFor(account.Plan))
		results <- verdict{"rule", err}
	}()
	close(start)
	accepted, refused, ruleSaved, overlaySaved := 0, 0, false, false
	for range 2 {
		result := <-results
		var aggregate *TrafficPolicyAggregateError
		if result.err == nil {
			accepted++
			ruleSaved = ruleSaved || result.kind == "rule"
			overlaySaved = overlaySaved || result.kind == "overlay"
		} else if errors.As(result.err, &aggregate) && aggregate.Scope == "host_compiled_projection_estimate" {
			refused++
		} else {
			t.Fatalf("unexpected %s race result: %v", result.kind, result.err)
		}
	}
	after, err := store.LatestEdgeRuleChangeID(t.Context())
	expectedChanges := int64(0)
	if ruleSaved {
		expectedChanges = 1
	}
	if err != nil || accepted != 1 || refused != 1 || after-before != expectedChanges {
		t.Fatalf("race intent/ledger rollback: accepted=%d refused=%d changes=%d err=%v", accepted, refused, after-before, err)
	}
	_, err = store.GetProjectEnvironmentEdgePolicy(t.Context(), account.ID, app.ID, "production")
	if overlaySaved && err != nil || !overlaySaved && !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused overlay retained intent: saved=%v err=%v", overlaySaved, err)
	}
	// Removing the expansion allows both writes, proving the refusal does not
	// leave locks, quota counters or rejected intent behind.
	if _, err := pool.Exec(t.Context(), `UPDATE edge_rules SET enabled=false WHERE account_id=$1`, account.ID); err != nil {
		t.Fatal(err)
	}
	if !overlaySaved {
		if _, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), policy); err != nil {
			t.Fatalf("overlay after repair: %v", err)
		}
	}
	if !ruleSaved {
		if _, err := store.CreateEdgeRuleIfUnderQuota(t.Context(), extra, api.MustLimitsFor(account.Plan)); err != nil {
			t.Fatalf("rule after repair: %v", err)
		}
	}
}

func TestPgTrafficEnvironmentNewScopeAndCloneRefuseLegacyOverload(t *testing.T) {
	store, pool, account, project, app, _ := trafficEnvironmentPGFixture(t)
	// Existing production explicitly suppresses the legacy fallback. A new
	// URL must fit on its own, even if the old unregistered selector language
	// was larger. The new empty overlay in the clone replaces these CORS rules.
	if _, err := pool.Exec(t.Context(), `INSERT INTO cors_presets(account_id,name,allow_origins,allow_methods,allow_headers,expose_headers)
		SELECT $1,'environment-'||g,ARRAY['*'],ARRAY['GET'],ARRAY[repeat('&',80000)],ARRAY[]::text[] FROM generate_series(1,140) g`, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action,cors_preset_id)
		SELECT $1,$2,'*','/',true,'cors','{"kind":"cors","cors":{}}',id FROM cors_presets WHERE account_id=$1`, account.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production"}); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateProjectEnvironment(t.Context(), ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_compiled_projection_estimate" {
		t.Fatalf("new URL accepted legacy fallback overload: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), account.ID, project.ID, "staging"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused registration persisted: %v", err)
	}
	if err := store.UpsertAppEnvInScope(t.Context(), account.ID, app.ID, "production", "CONFIG", "source"); err != nil {
		t.Fatal(err)
	}
	clone := ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "clone", ShareResources: true}
	// A legacy source overlay plus a retained route lowers the old unknown
	// host aggregate, but still does not fit the newly registered URL. Each
	// copied overlay fits its separate canonical contract limit.
	action := EdgeRuleAction{Kind: EdgeRuleKindRoute, Route: &EdgeRuleRouteAction{TargetAppSlug: app.Slug},
		Validate: &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 507) + "1e130000]")}}
	encodedAction, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,'*','/',true,'route',$3::jsonb)`, account.ID, app.ID, encodedAction); err != nil {
		t.Fatal(err)
	}
	rules := trafficEnvironmentHeaders(20000)
	encoded, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE project_environment_edge_policies SET rules=$2::jsonb WHERE app_id=$1`, app.ID, encoded); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.CloneProjectEnvironment(t.Context(), clone, api.MustLimitsFor(account.Plan))
	if !errors.As(err, &aggregate) || aggregate.Scope != "host_compiled_projection_estimate" {
		t.Fatalf("clone accepted new host aggregate overload: %v", err)
	}
	if _, err := store.ProjectEnvironmentBySlug(t.Context(), account.ID, project.ID, clone.TargetSlug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused clone retained registration: %v", err)
	}
	var rows int
	if err := pool.QueryRow(t.Context(), `SELECT (SELECT count(*) FROM app_envs WHERE app_id=$1 AND scope=$2)+(SELECT count(*) FROM project_environment_edge_policies WHERE app_id=$1 AND environment_slug=$2)`, app.ID, clone.TargetSlug).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("refused clone partial rows=%d err=%v", rows, err)
	}
	if _, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production"}); err != nil {
		t.Fatalf("source repair: %v", err)
	}
	_, result, err := store.CloneProjectEnvironment(t.Context(), clone, api.MustLimitsFor(account.Plan))
	if err != nil || result.VariablesCopied != 1 || result.PoliciesCopied != 1 {
		t.Fatalf("clone after repair: result=%+v err=%v", result, err)
	}
}
