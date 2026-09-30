//go:build !no_pg

// adr: 375
package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func trafficAppActivity(account Account, kind string) OrgActivity {
	actor := uuid.MustParse(account.ID)
	return OrgActivity{Kind: kind, ActorType: OrgActivityActorUser, ActorAccountID: &actor,
		ActorLabel: account.Email, ResourceType: "app", SourceType: kind, SourceID: "traffic-activation",
		Data: json.RawMessage(`{"phase":"traffic-test"}`)}
}

func trafficAppIntentCounts(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var counts string
	err := pool.QueryRow(t.Context(), `SELECT jsonb_build_object(
		'apps',(SELECT count(*) FROM apps),'projects',(SELECT count(*) FROM projects),
		'environments',(SELECT count(*) FROM project_environments),
		'crons',(SELECT count(*) FROM crons),'activity',(SELECT count(*) FROM org_activity_outbox),
		'preview_sets',(SELECT count(*) FROM pr_preview_sets))::text`).Scan(&counts)
	if err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestPgTrafficAppActivationRefusesNewEnvironmentOverload(t *testing.T) {
	testPgTrafficAppActivation(t, false)
}

func TestPgTrafficAppActivationRefusesNewPrimaryOverload(t *testing.T) {
	testPgTrafficAppActivation(t, true)
}

func testPgTrafficAppActivation(t *testing.T, primary bool) {
	for _, operation := range []string{"create", "quota_create", "activity_create", "preview_batch", "preview_set", "project_plan", "reconcile_create", "restore", "activity_restore", "reconcile_restore", "status_update", "activity_status_update", "status_cas", "visibility_update", "activity_visibility_update"} {
		t.Run(operation, func(t *testing.T) {
			store, pool, account, project, source, _ := trafficEnvironmentPGFixture(t)
			if primary {
				store = NewPgStore(pool, WithTrafficAppsDomain(".APPS.EXAMPLE.TEST "))
				// No registered environments: this refusal must come solely
				// from the configured ordinary primary URL.
				if _, err := pool.Exec(t.Context(), `DELETE FROM project_environments WHERE project_id=$1`, project.ID); err != nil {
					t.Fatal(err)
				}
			}
			limits := api.MustLimitsFor(account.Plan)
			target := App{AccountID: account.ID, ProjectID: project.ID, Slug: "traffic-new-web", WorkloadName: "new-web", WorkloadClass: WorkloadClassHTTP, Status: AppActive}
			needsTombstone := strings.Contains(operation, "restore") || strings.Contains(operation, "status")
			if strings.Contains(operation, "visibility") {
				target.Visibility = api.AppVisibilityInternal
				var err error
				target, err = store.CreateApp(t.Context(), target)
				if err != nil {
					t.Fatal(err)
				}
			}
			if needsTombstone {
				var err error
				target, err = store.CreateApp(t.Context(), target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.CreateCron(t.Context(), target.ID, "*/5 * * * *", "/job", true); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(operation, "restore") {
					target, err = store.ScheduleAppDeletion(t.Context(), target.ID, time.Now().Add(time.Hour))
					if err != nil {
						t.Fatal(err)
					}
				} else {
					// Generic status writers can encounter status-only tombstones.
					// They become registered URL owners when status changes back.
					if _, err := pool.Exec(t.Context(), `UPDATE apps SET status='deleted' WHERE id=$1`, target.ID); err != nil {
						t.Fatal(err)
					}
					// The database deletion trigger stamps its grace metadata.
					target, err = store.AppByID(t.Context(), target.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if operation == "preview_batch" || operation == "preview_set" {
				expires := time.Now().Add(time.Hour)
				target.Slug = "pr-42-traffic-new-web"
				target.PreviewOfSlug = source.Slug
				target.PreviewPrNumber = 42
				target.PreviewPrState = PreviewPrStateOpen
				target.PreviewExpiresAt = &expires
			}
			// Seed one small-on-input legacy row whose canonical JSONB expands
			// beyond the runtime read cap. It already affects old URLs; only
			// the new registered scope must be refused by activation.
			ruleID := uuid.NewString()
			matchHost := "*"
			if primary {
				matchHost = target.Slug + ".apps.example.test"
			}
			action := EdgeRuleAction{Kind: EdgeRuleKindRoute, Route: &EdgeRuleRouteAction{TargetAppSlug: source.Slug},
				Validate: &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 519) + "1e130000]")}}
			encoded, err := json.Marshal(action)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), `INSERT INTO edge_rules(id,account_id,app_id,match_host,match_path,enabled,kind,action) VALUES($1,$2,$3,$4,'/',true,'route',$5::jsonb)`, ruleID, account.ID, source.ID, matchHost, encoded); err != nil {
				t.Fatal(err)
			}
			before := trafficAppIntentCounts(t, pool)
			apply := func() error {
				switch operation {
				case "create":
					_, err := store.CreateApp(t.Context(), target)
					return err
				case "quota_create":
					_, err := store.CreateAppIfUnderQuota(t.Context(), target, limits)
					return err
				case "activity_create":
					_, _, err := store.CreateAppIfUnderQuotaWithActivity(t.Context(), target, limits, trafficAppActivity(account, "app.created"))
					return err
				case "preview_batch":
					_, err := store.CreatePRPreviewAppsIfUnderQuota(t.Context(), []App{target}, limits)
					return err
				case "preview_set":
					_, err := store.ReservePRPreviewSet(t.Context(), PRPreviewHead{InstallationID: 7, RepoFullName: "example/traffic", PRNumber: 42, CommitSHA: strings.Repeat("a", 40)}, []App{target}, limits)
					return err
				case "project_plan":
					_, _, _, err := store.ApplyProjectPlan(t.Context(), Project{AccountID: account.ID, Slug: "traffic-plan", ScanSource: ProjectScanSourceCompose}, []App{target}, nil, limits)
					return err
				case "reconcile_create", "reconcile_restore":
					_, err := store.ApplyProjectReconcile(t.Context(), project, []ProjectReconcileMutation{{Op: "create", App: target}}, []ProjectReconcileCron{{WorkloadName: target.WorkloadName, Schedule: "*/10 * * * *", Path: "/new", Enabled: true}}, ProjectScanSourceCompose, limits)
					return err
				case "restore":
					_, err := store.RestoreApp(t.Context(), target.ID, limits)
					return err
				case "activity_restore":
					_, _, err := store.RestoreAppWithActivity(t.Context(), target.ID, limits, trafficAppActivity(account, "app.restored"))
					return err
				case "status_update":
					active := AppActive
					_, err := store.UpdateApp(t.Context(), target.ID, UpdateAppParams{Status: &active})
					return err
				case "activity_status_update":
					active := AppActive
					_, _, err := store.UpdateAppWithActivity(t.Context(), target.ID, UpdateAppParams{Status: &active}, trafficAppActivity(account, "app.updated"), func(App, App) (json.RawMessage, bool, error) { return json.RawMessage(`{"field":"status"}`), true, nil })
					return err
				case "visibility_update":
					public := api.AppVisibilityPublic
					_, err := store.UpdateApp(t.Context(), target.ID, UpdateAppParams{SetVisibility: true, Visibility: &public})
					return err
				case "activity_visibility_update":
					public := api.AppVisibilityPublic
					_, _, err := store.UpdateAppWithActivity(t.Context(), target.ID, UpdateAppParams{SetVisibility: true, Visibility: &public}, trafficAppActivity(account, "app.updated"), func(App, App) (json.RawMessage, bool, error) {
						return json.RawMessage(`{"field":"visibility"}`), true, nil
					})
					return err
				case "status_cas":
					changed, err := store.CompareAndSetAppStatus(t.Context(), target.ID, AppDeleted, AppActive)
					if err != nil && changed {
						t.Fatal("rejected CAS reported success")
					}
					return err
				default:
					t.Fatal(operation)
					return nil
				}
			}
			err = apply()
			var aggregate *TrafficPolicyAggregateError
			if !errors.As(err, &aggregate) || aggregate.Scope != "host_rule_projection" {
				t.Fatalf("activation accepted new overloaded URL: %v", err)
			}
			if primary && aggregate.Host != matchHost {
				t.Fatalf("primary witness=%q want=%q", aggregate.Host, matchHost)
			}
			if after := trafficAppIntentCounts(t, pool); after != before {
				t.Fatalf("rejected activation retained intent: before=%s after=%s", before, after)
			}
			if needsTombstone {
				saved, err := store.AppByID(t.Context(), target.ID)
				if err != nil || saved.Status != AppDeleted || !trafficAppTimesEqual(saved.DeleteGraceUntil, target.DeleteGraceUntil) {
					t.Fatalf("rejected activation changed tombstone: status=%s err=%v", saved.Status, err)
				}
				if strings.Contains(operation, "restore") {
					var suspended string
					if err := pool.QueryRow(t.Context(), `SELECT suspended_reason FROM crons WHERE app_id=$1`, target.ID).Scan(&suspended); err != nil || suspended != CronSuspendedAppDeleted {
						t.Fatalf("rejected restore resumed cron: %q err=%v", suspended, err)
					}
				}
			}
			if strings.Contains(operation, "visibility") {
				saved, err := store.AppByID(t.Context(), target.ID)
				if err != nil || saved.Visibility != api.AppVisibilityInternal {
					t.Fatalf("refused publication changed visibility: %s err=%v", saved.Visibility, err)
				}
			}
			if err := store.DeleteEdgeRule(t.Context(), ruleID); err != nil {
				t.Fatal(err)
			}
			if err := apply(); err != nil {
				t.Fatalf("activation after policy repair: %v", err)
			}
			if strings.Contains(operation, "visibility") {
				saved, err := store.AppByID(t.Context(), target.ID)
				if err != nil || saved.Visibility != api.AppVisibilityPublic {
					t.Fatalf("successful publication did not change visibility: %s err=%v", saved.Visibility, err)
				}
			}
			if needsTombstone {
				saved, err := store.AppByID(t.Context(), target.ID)
				if err != nil || saved.Status != AppActive {
					t.Fatalf("successful activation did not reactivate app: status=%s err=%v", saved.Status, err)
				}
			}
		})
	}
}

func trafficAppTimesEqual(first, second *time.Time) bool {
	return first == nil && second == nil || first != nil && second != nil && first.Equal(*second)
}

func TestPgTrafficAppRestoreAndRuleRaceSharesEnvironmentAllowance(t *testing.T) {
	store, pool, account, project, _, _ := trafficEnvironmentPGFixture(t)
	target, err := store.CreateApp(t.Context(), App{AccountID: account.ID, ProjectID: project.ID, Slug: "traffic-restore-race", WorkloadName: "restore-race", Status: AppActive})
	if err != nil {
		t.Fatal(err)
	}
	base := trafficHostRule(account, target, "*")
	base.Action.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 507) + "1e130000]")}
	baseline, err := store.CreateEdgeRule(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentEdgePolicy(t.Context(), ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: target.ID, EnvironmentSlug: "production", Rules: trafficEnvironmentHeaders(8000)}); err != nil {
		t.Fatal(err)
	}
	extra, err := store.CreateEdgeRule(t.Context(), trafficHostRule(account, target, "*"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ScheduleAppDeletion(t.Context(), target.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	before, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	changed := extra.Action
	changed.Validate = &EdgeRuleValidateAction{Schema: json.RawMessage("[" + strings.Repeat("1e130000,", 3) + "1e130000]")}
	type verdict struct {
		kind string
		err  error
	}
	start, results := make(chan struct{}), make(chan verdict, 2)
	limits := api.MustLimitsFor(account.Plan)
	go func() {
		<-start
		_, _, err := store.RestoreAppWithActivity(t.Context(), target.ID, limits, trafficAppActivity(account, "app.restored"))
		results <- verdict{"restore", err}
	}()
	go func() {
		<-start
		_, err := store.UpdateEdgeRule(t.Context(), extra.ID, UpdateEdgeRuleParams{Action: &changed})
		results <- verdict{"rule", err}
	}()
	close(start)
	accepted, refused, restored, ruleSaved := 0, 0, false, false
	for range 2 {
		result := <-results
		var aggregate *TrafficPolicyAggregateError
		if result.err == nil {
			accepted++
			restored = restored || result.kind == "restore"
			ruleSaved = ruleSaved || result.kind == "rule"
		} else if errors.As(result.err, &aggregate) && aggregate.Scope == "host_compiled_projection_estimate" {
			refused++
		} else {
			t.Fatalf("unexpected %s race result: %v", result.kind, result.err)
		}
	}
	if accepted != 1 || refused != 1 {
		t.Fatalf("restore/rule budget race accepted=%d refused=%d", accepted, refused)
	}
	saved, err := store.AppByID(t.Context(), target.ID)
	if err != nil || restored != (saved.Status == AppActive) {
		t.Fatalf("rejected restore changed app: restored=%v status=%s err=%v", restored, saved.Status, err)
	}
	var outbox int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM org_activity_outbox`).Scan(&outbox); err != nil || restored != (outbox == 1) {
		t.Fatalf("rejected restore retained activity: restored=%v rows=%d err=%v", restored, outbox, err)
	}
	after, err := store.LatestEdgeRuleChangeID(t.Context())
	if err != nil || ruleSaved != (after == before+1) || !ruleSaved && after != before {
		t.Fatalf("rejected rule retained change: saved=%v before=%d after=%d err=%v", ruleSaved, before, after, err)
	}
	if err := store.DeleteEdgeRule(t.Context(), baseline.ID); err != nil {
		t.Fatal(err)
	}
	if !restored {
		if _, _, err := store.RestoreAppWithActivity(t.Context(), target.ID, limits, trafficAppActivity(account, "app.restored")); err != nil {
			t.Fatalf("restore after repair: %v", err)
		}
	}
	if !ruleSaved {
		if _, err := store.UpdateEdgeRule(t.Context(), extra.ID, UpdateEdgeRuleParams{Action: &changed}); err != nil {
			t.Fatalf("rule after repair: %v", err)
		}
	}
}
