// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

func memTrafficFixture(t *testing.T) (*MemStore, Account, Project, App, ProjectEnvironment) {
	t.Helper()
	m := NewMemStore()
	owner, err := m.CreateAccountWithPersonalOrg(t.Context(), CreateAccountWithPersonalOrgParams{Email: "mem-traffic@example.test", Plan: api.PlanScale})
	if err != nil {
		t.Fatal(err)
	}
	project, err := m.CreateProject(t.Context(), Project{AccountID: owner.Account.ID, Slug: "mem-traffic", ScanSource: ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := m.CreateApp(t.Context(), App{AccountID: owner.Account.ID, ProjectID: project.ID, Slug: "mem-traffic-web", WorkloadName: "web"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := m.ProjectEnvironmentBySlug(t.Context(), owner.Account.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	return m, owner.Account, project, app, environment
}

func memTrafficRule(account Account, app App, host string, numbers int) CreateEdgeRuleParams {
	action := EdgeRuleAction{Kind: EdgeRuleKindRoute, Route: &EdgeRuleRouteAction{TargetAppSlug: app.Slug}}
	if numbers > 0 {
		action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", numbers-1) + "1e130000]")}
	}
	return CreateEdgeRuleParams{AccountID: account.ID, AppID: app.ID, MatchHost: host, MatchPath: "/", Enabled: true, Kind: EdgeRuleKindRoute, Action: action}
}

func requireMemTrafficAggregate(t *testing.T, err error, scope string) {
	t.Helper()
	var aggregate *TrafficPolicyAggregateError
	if !errors.As(err, &aggregate) || aggregate.Scope != scope || aggregate.Observed <= aggregate.Limit {
		t.Fatalf("expected %s aggregate refusal: %v", scope, err)
	}
}

func TestMemTrafficHostOverlapMutationAndRepair(t *testing.T) {
	for _, operation := range []string{"create", "quota_create", "retarget", "enable"} {
		t.Run(operation, func(t *testing.T) {
			m, account, _, app, _ := memTrafficFixture(t)
			first, err := m.CreateEdgeRule(t.Context(), memTrafficRule(account, app, "api.*", 260))
			if err != nil {
				t.Fatal(err)
			}
			in := memTrafficRule(account, app, "*.test", 260)
			var second EdgeRule
			if operation == "retarget" || operation == "enable" {
				if operation == "retarget" {
					in.MatchHost = "web.*"
				} else {
					in.Enabled = false
				}
				second, err = m.CreateEdgeRule(t.Context(), in)
				if err != nil {
					t.Fatal(err)
				}
			}
			apply := func() error {
				switch operation {
				case "create":
					_, err := m.CreateEdgeRule(t.Context(), in)
					return err
				case "quota_create":
					_, err := m.CreateEdgeRuleIfUnderQuota(t.Context(), in, api.MustLimitsFor(account.Plan))
					return err
				case "retarget":
					host := "*.test"
					_, err := m.UpdateEdgeRule(t.Context(), second.ID, UpdateEdgeRuleParams{MatchHost: &host})
					return err
				default:
					enabled := true
					_, err := m.UpdateEdgeRule(t.Context(), second.ID, UpdateEdgeRuleParams{Enabled: &enabled})
					return err
				}
			}
			before := len(m.edgeRules)
			requireMemTrafficAggregate(t, apply(), "host_rule_projection")
			if len(m.edgeRules) != before || second.ID != "" && !reflect.DeepEqual(m.edgeRules[second.ID], second) {
				t.Fatal("rejected mutation changed stored intent")
			}
			if err := m.DeleteEdgeRule(t.Context(), first.ID); err != nil {
				t.Fatal(err)
			}
			if err := apply(); err != nil {
				t.Fatalf("repair/retry: %v", err)
			}
		})
	}
}

func TestMemTrafficHostConcurrentAllowance(t *testing.T) {
	m, account, project, app, _ := memTrafficFixture(t)
	peer, err := m.CreateApp(t.Context(), App{AccountID: account.ID, ProjectID: project.ID, Slug: "mem-traffic-peer"})
	if err != nil {
		t.Fatal(err)
	}
	start, results := make(chan struct{}), make(chan error, 2)
	for _, owner := range []App{app, peer} {
		go func() {
			<-start
			_, err := m.CreateEdgeRule(t.Context(), memTrafficRule(account, owner, "*", 260))
			results <- err
		}()
	}
	close(start)
	accepted := 0
	for range 2 {
		if err := <-results; err != nil {
			requireMemTrafficAggregate(t, err, "host_rule_projection")
		} else {
			accepted++
		}
	}
	if accepted != 1 || len(m.edgeRules) != 1 {
		t.Fatalf("shared allowance: accepted=%d rows=%d", accepted, len(m.edgeRules))
	}
}

func TestMemTrafficHostSelectorRuntimeParity(t *testing.T) {
	for _, test := range []struct {
		pattern, host string
		match         bool
	}{
		{"api.*", "api.example.test", true}, {"*.test", ".test", true}, {"*.test", "test", false},
		{"?pi.test", "épi.test", true}, {"?pi.test", "api.test", true}, {"?pi.test", "pi.test", false},
		{"api.%", "api.example.test", true}, {"_pi.test", "api.test", true},
		{`api.\%`, "api.%", true}, {`api.\%`, "api.test", false}, {`api.\%`, `api.\%`, true},
		{`api.\?`, "api._", true}, {`api.\*`, "api.%", true}, {"a*b", "a\nb", true},
		{"a*?*b", "ab", false}, {"a*?*b", "acb", true}, {`api.test\`, "api.test", false},
	} {
		t.Run(test.pattern+"/"+test.host, func(t *testing.T) {
			m := NewMemStore()
			m.edgeRules["selector"] = EdgeRule{ID: "selector", Enabled: true, MatchHost: test.pattern}
			rules, err := m.MatchEdgeRulesForHost(t.Context(), test.host)
			if err != nil || (len(rules) == 1) != test.match {
				t.Fatalf("runtime selector %q/%q: rules=%d want=%t err=%v", test.pattern, test.host, len(rules), test.match, err)
			}
		})
	}
}

func memTrafficActivity(account Account, kind string) OrgActivity {
	actor := uuid.MustParse(account.ID)
	return OrgActivity{Kind: kind, ActorType: OrgActivityActorUser, ActorAccountID: &actor,
		ActorLabel: account.Email, ResourceType: "app", SourceType: kind, SourceID: "mem-traffic-activation",
		Data: json.RawMessage(`{"phase":"traffic-test"}`)}
}

func memTrafficIntentCounts(m *MemStore) []int {
	return []int{len(m.apps), len(m.projects), len(m.projectEnvironments), len(m.crons), len(m.orgActivityOutbox), len(m.previewSets), len(m.envs), len(m.projectEnvironmentEdgePolicies)}
}

func TestMemTrafficAppActivationAndEnvironmentRegistrationRollback(t *testing.T) {
	for _, operation := range []string{"create", "quota_create", "activity_create", "preview_batch", "preview_set", "project_plan", "reconcile_create", "restore", "activity_restore", "reconcile_restore", "status_update", "activity_status_update", "status_cas", "visibility_update", "activity_visibility_update", "environment", "clone"} {
		t.Run(operation, func(t *testing.T) {
			m, account, project, source, _ := memTrafficFixture(t)
			limits := api.MustLimitsFor(account.Plan)
			target := App{AccountID: account.ID, ProjectID: project.ID, Slug: "mem-new-web", WorkloadName: "new-web", Status: AppActive}
			needsTombstone := strings.Contains(operation, "restore") || strings.Contains(operation, "status")
			if needsTombstone || strings.Contains(operation, "visibility") {
				if !needsTombstone {
					target.Visibility = api.AppVisibilityInternal
				}
				var err error
				target, err = m.CreateApp(t.Context(), target)
				if err != nil {
					t.Fatal(err)
				}
				if needsTombstone {
					if _, err := m.CreateCron(t.Context(), target.ID, "*/5 * * * *", "/job", true); err != nil {
						t.Fatal(err)
					}
					target, err = m.ScheduleAppDeletion(t.Context(), target.ID, time.Now().Add(time.Hour))
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if strings.HasPrefix(operation, "preview_") {
				expires := time.Now().Add(time.Hour)
				target.Slug, target.PreviewOfSlug, target.PreviewPrNumber = "pr-42-mem-new-web", source.Slug, 42
				target.PreviewPrState, target.PreviewExpiresAt = PreviewPrStateOpen, &expires
			}
			legacy := memTrafficRule(account, source, "*", 520)
			m.edgeRules["legacy"] = EdgeRule{ID: "legacy", AccountID: account.ID, AppID: source.ID, MatchHost: "*", MatchPath: "/", Enabled: true, Kind: legacy.Kind, Action: legacy.Action}
			before := memTrafficIntentCounts(m)
			apply := func() error {
				switch operation {
				case "create":
					_, err := m.CreateApp(t.Context(), target)
					return err
				case "quota_create":
					_, err := m.CreateAppIfUnderQuota(t.Context(), target, limits)
					return err
				case "activity_create":
					_, _, err := m.CreateAppIfUnderQuotaWithActivity(t.Context(), target, limits, memTrafficActivity(account, "app.created"))
					return err
				case "preview_batch":
					_, err := m.CreatePRPreviewAppsIfUnderQuota(t.Context(), []App{target}, limits)
					return err
				case "preview_set":
					_, err := m.ReservePRPreviewSet(t.Context(), PRPreviewHead{InstallationID: 7, RepoFullName: "example/mem-traffic", PRNumber: 42, CommitSHA: strings.Repeat("a", 40)}, []App{target}, limits)
					return err
				case "project_plan":
					_, _, _, err := m.ApplyProjectPlan(t.Context(), Project{AccountID: account.ID, Slug: "mem-plan", ScanSource: ProjectScanSourceCompose}, []App{target}, []Cron{{AppID: source.ID, Schedule: "*/10 * * * *", Path: "/new"}}, limits)
					return err
				case "reconcile_create", "reconcile_restore":
					_, err := m.ApplyProjectReconcile(t.Context(), project, []ProjectReconcileMutation{{Op: "create", App: target}}, []ProjectReconcileCron{{WorkloadName: target.WorkloadName, Schedule: "*/10 * * * *", Path: "/new", Enabled: true}}, ProjectScanSourceCompose, limits)
					return err
				case "restore":
					_, err := m.RestoreApp(t.Context(), target.ID, limits)
					return err
				case "activity_restore":
					_, _, err := m.RestoreAppWithActivity(t.Context(), target.ID, limits, memTrafficActivity(account, "app.restored"))
					return err
				case "status_update", "activity_status_update", "visibility_update", "activity_visibility_update":
					active, public := AppActive, api.AppVisibilityPublic
					p := UpdateAppParams{Status: &active}
					if strings.Contains(operation, "visibility") {
						p = UpdateAppParams{SetVisibility: true, Visibility: &public}
					}
					if strings.HasPrefix(operation, "activity_") {
						_, _, err := m.UpdateAppWithActivity(t.Context(), target.ID, p, memTrafficActivity(account, "app.updated"), func(App, App) (json.RawMessage, bool, error) {
							return json.RawMessage(`{"field":"activation"}`), true, nil
						})
						return err
					}
					_, err := m.UpdateApp(t.Context(), target.ID, p)
					return err
				case "status_cas":
					changed, err := m.CompareAndSetAppStatus(t.Context(), target.ID, AppDeleted, AppActive)
					if changed && err != nil {
						t.Fatal("refused CAS reported success")
					}
					return err
				case "environment":
					_, err := m.CreateProjectEnvironment(t.Context(), ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
					return err
				default:
					_, _, err := m.CloneProjectEnvironment(t.Context(), ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging"}, limits)
					return err
				}
			}
			requireMemTrafficAggregate(t, apply(), "host_rule_projection")
			if !reflect.DeepEqual(before, memTrafficIntentCounts(m)) {
				t.Fatalf("rejected activation retained intent: before=%v after=%v", before, memTrafficIntentCounts(m))
			}
			if target.ID != "" && !reflect.DeepEqual(m.apps[target.ID], target) {
				t.Fatal("rejected activation changed tombstone or visibility")
			}
			if needsTombstone {
				for _, cron := range m.crons {
					if cron.AppID == target.ID && cron.SuspendedReason != CronSuspendedAppDeleted {
						t.Fatal("rejected activation resumed cron")
					}
				}
			}
			if err := m.DeleteEdgeRule(t.Context(), "legacy"); err != nil {
				t.Fatal(err)
			}
			if err := apply(); err != nil {
				t.Fatalf("repair/retry: %v", err)
			}
		})
	}
}

func TestMemTrafficEnvironmentProjectionMatchesGoCompiler(t *testing.T) {
	m, account, project, app, environment := memTrafficFixture(t)
	host := hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, environment.ID, app.ID)
	preset, err := m.CreateCorsPresetIfUnderQuota(t.Context(), CorsPreset{AccountID: account.ID, Name: "shared", Description: "<>&", AllowOrigins: []string{"*"}}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	in := memTrafficRule(account, app, host, 1)
	if _, err := m.CreateEdgeRule(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	in.Kind, in.Action = EdgeRuleKindCORSA, EdgeRuleAction{Kind: EdgeRuleKindCORSA, CORS: &EdgeRuleCORSAction{CorsPresetID: &preset.ID}}
	for range 2 {
		if _, err := m.CreateEdgeRule(t.Context(), in); err != nil {
			t.Fatal(err)
		}
	}
	for _, overlay := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "explicit-empty"}[overlay], func(t *testing.T) {
			var policy *ProjectEnvironmentEdgePolicy
			if overlay {
				stored, err := m.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: environment.Slug})
				if err != nil {
					t.Fatal(err)
				}
				policy = &stored
			}
			m.mu.Lock()
			view, err := m.readMemTrafficHostAnalysisLocked(t.Context(), account.ID, memTrafficPolicyChange{})
			m.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(map[int]bool)
			for index := range view.Groups {
				accepted[index] = true // Every fixture selector is this exact host.
			}
			totals := environmentHostTotals(view, accepted, &view.Environments[0])
			var originals []EdgeRule
			for _, rule := range m.edgeRules {
				originals = append(originals, rule)
			}
			rules := ProjectEnvironmentPolicyRules(host, environment, app, originals, policy)
			encoded, err := json.Marshal(rules)
			if err != nil {
				t.Fatal(err)
			}
			compiled := int64(len(encoded))
			if !overlay {
				asset, _ := json.Marshal(preset)
				compiled += int64(len(asset))
			}
			if totals.compiled < compiled || totals.compiled-compiled > int64(len(rules))+1 || totals.compiledRows != int64(len(rules)) {
				t.Fatalf("Go compiler projection mismatch: totals=%+v actual=%d rows=%d", totals, compiled, len(rules))
			}
			if totals.canonical < 130000 || totals.compiled > 130000 {
				t.Fatal("compact RawMessage number was expanded for the Go compiler or omitted from the canonical bound")
			}
		})
	}
}

func TestMemTrafficCanceledMutationRetainsIntent(t *testing.T) {
	m, account, _, app, _ := memTrafficFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.CreateEdgeRule(ctx, memTrafficRule(account, app, "*", 0)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write: %v", err)
	}
	if len(m.edgeRules) != 0 {
		t.Fatal("canceled write persisted")
	}
}

func TestMemTrafficCompilerGrowthRefusalAndRepair(t *testing.T) {
	for _, operation := range []string{"preset", "overlay"} {
		t.Run(operation, func(t *testing.T) {
			m, account, project, app, environment := memTrafficFixture(t)
			preset, err := m.CreateCorsPresetIfUnderQuota(t.Context(), CorsPreset{AccountID: account.ID, Name: "grow", AllowOrigins: []string{"*"}}, api.MustLimitsFor(account.Plan))
			if err != nil {
				t.Fatal(err)
			}
			in := memTrafficRule(account, app, "*", 0)
			in.Kind, in.Action = EdgeRuleKindCORSA, EdgeRuleAction{Kind: EdgeRuleKindCORSA, CORS: &EdgeRuleCORSAction{CorsPresetID: &preset.ID}}
			if _, err := m.CreateEdgeRule(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			// An actual large Go compiler input, rather than a compact numeric
			// input that expands only in the conservative canonical estimator.
			baseline := memTrafficRule(account, app, "*", 0)
			baseline.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage(`"` + strings.Repeat("x", api.TrafficPolicyMaxHostBytes-300000) + `"`)}
			m.edgeRules["compiler-baseline"] = EdgeRule{ID: "compiler-baseline", AccountID: account.ID, AppID: app.ID, MatchHost: "*", MatchPath: "/", Enabled: true, Kind: baseline.Kind, Action: baseline.Action}
			apply := func() error {
				if operation == "preset" {
					changed := preset
					changed.AllowOrigins = []string{strings.Repeat("x", 350000)}
					_, err := m.UpdateCorsPreset(t.Context(), account.ID, preset.ID, changed)
					return err
				}
				rules := make([]ProjectEnvironmentEdgeRule, 20)
				for index := range rules {
					rules[index] = ProjectEnvironmentEdgeRule{Kind: EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
						Action: EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Grow", Action: "set", Value: strings.Repeat("&", 2800)}}}}}
				}
				_, err := m.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: environment.Slug, Rules: rules})
				return err
			}
			requireMemTrafficAggregate(t, apply(), "host_compiled_projection_estimate")
			if !reflect.DeepEqual(m.corsPresets[preset.ID], preset) || len(m.projectEnvironmentEdgePolicies) != 0 {
				t.Fatal("rejected compiler growth changed preset/overlay intent")
			}
			if err := m.DeleteEdgeRule(t.Context(), "compiler-baseline"); err != nil {
				t.Fatal(err)
			}
			if err := apply(); err != nil {
				t.Fatalf("compiler repair/retry: %v", err)
			}
		})
	}
}
