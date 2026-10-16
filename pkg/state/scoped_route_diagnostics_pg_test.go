//go:build !no_pg

package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestScopedRouteDiagnosticsPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	s := state.NewPgStore(pool)
	acct, app, def, stage := healthFixture(t, s)
	project, err := s.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "diagnostics"})
	if err != nil {
		t.Fatal(err)
	}
	productionMonitorExec(t, pool, `UPDATE apps SET project_id=$1 WHERE id=$2`, project.ID, app.ID)
	stageEnv, err := s.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	prodEnv, err := s.ProjectEnvironmentBySlug(t.Context(), acct.ID, project.ID, "production")
	if err != nil {
		t.Fatal(err)
	}
	prod, err := s.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:prod"})
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ deployment, environment, scope string }{{stage.ID, stageEnv.ID, "staging"}, {prod.ID, prodEnv.ID, "production"}} {
		productionMonitorExec(t, pool, `UPDATE deployments SET scope=$1 WHERE id=$2`, pair.scope, pair.deployment)
		spec := uuid.NewString()
		productionMonitorExec(t, pool, `INSERT INTO project_environment_workload_specs(id,environment_id,app_id,revision,config_hash,settings) VALUES($1,$2,$3,1,$4,'{}')`, spec, pair.environment, app.ID, strings.Repeat("a", 64))
		productionMonitorExec(t, pool, `INSERT INTO project_environment_workload_deployment_specs(deployment_id,spec_id) VALUES($1,$2)`, pair.deployment, spec)
	}
	role := "compute-node"
	node, err := s.CreateComputeNode(t.Context(), state.ComputeNode{Name: "diagnostic-node", TargetURL: "tcp://127.0.0.1:50051", VPCPUs: 1, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 512, VCPUBudget: 1, Active: true, Role: &role})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Minute).Add(-time.Hour)
	consumer := insertAdviceConsumer(t, pool, acct.ID, app.ID, "diagnostic-client")
	for _, row := range []struct {
		deployment string
		n          int
	}{{def.ID, 3}, {stage.ID, 30}, {prod.ID, 900}} {
		inst, err := s.CreateInstance(t.Context(), app.ID, row.deployment, string(state.StateRunning), 128, node.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		insertAwakeUsage(t, pool, acct.ID, app.ID, inst.ID, at, int64(row.n), 1)
		productionMonitorExec(t, pool, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,consumer_id,content_outcome,received_at) VALUES($1,$2,$3,'GET /shared','GET',200,5,$4,$5,'deployment_v1:same_60',$6)`, acct.ID, app.ID, row.deployment, row.n, consumer, at)
	}
	// Legacy observations have no isolated comparison provenance. They still
	// count as scoped traffic, but cannot become learned content evidence.
	productionMonitorExec(t, pool, `INSERT INTO request_telemetry(account_id,app_id,deployment_id,route,method,status,latency_ms,count,consumer_id,content_outcome,received_at) VALUES($1,$2,$3,'GET /shared','GET',200,5,100,$4,'changed_60',$5)`, acct.ID, app.ID, stage.ID, consumer, at)
	// Neither instance-only nor missing-deployment evidence can be attributed.
	insertAwakeUsage(t, pool, acct.ID, app.ID, uuid.NewString(), at, 444, 1)
	insertAwakeTelemetry(t, pool, acct.ID, []awakeTelemetry{{app: app.ID, deployment: nilDeployment, method: "GET", route: "GET /private", status: 200, count: 1000, at: at}})
	since, until := pgtype.Timestamptz{Time: at.Add(-time.Minute), Valid: true}, pgtype.Timestamptz{Time: at.Add(time.Minute), Valid: true}
	accountID, appID := pgUUID(t, acct.ID), pgUUID(t, app.ID)
	for _, tc := range []struct {
		scope, env            string
		requests, mb, content int64
	}{{"default", "", 3, 3, 3}, {"staging", stageEnv.ID, 130, 30, 30}, {"production", prodEnv.ID, 900, 900, 900}} {
		t.Run(tc.scope, func(t *testing.T) {
			stats, err := s.RouteAdviceRouteStats(t.Context(), sqlc.RouteAdviceRouteStatsParams{AccountID: accountID, AppID: appID, SelectedScope: tc.scope, SelectedEnvironmentID: tc.env, SinceAt: since, UntilAt: until, RouteLimit: 10, CacheMaxAgeSeconds: 60})
			if err != nil || len(stats) != 1 || stats[0].Requests != tc.requests || stats[0].ContentSame60 != tc.content || stats[0].ContentChanged60 != 0 {
				t.Fatalf("scoped stats=%+v err=%v", stats, err)
			}
			consumers, err := s.RouteAdviceConsumers(t.Context(), sqlc.RouteAdviceConsumersParams{AccountID: accountID, AppID: appID, SelectedScope: tc.scope, SelectedEnvironmentID: tc.env, SinceAt: since, UntilAt: until})
			if err != nil || len(consumers) != 1 || consumers[0].Requests != tc.requests {
				t.Fatalf("scoped consumers=%+v err=%v", consumers, err)
			}
			excess, err := s.RouteAdviceThrottleExcess(t.Context(), sqlc.RouteAdviceThrottleExcessParams{AccountID: accountID, AppID: appID, SelectedScope: tc.scope, SelectedEnvironmentID: tc.env, SinceAt: since, UntilAt: until, ConsumerID: pgUUID(t, consumer), Route: "GET /shared", Method: "GET", AllowancePerMinute: 1})
			if err != nil || excess != tc.requests-1 {
				t.Fatalf("scoped excess=%d err=%v", excess, err)
			}
			usage, err := s.RouteAwakeMinutes(t.Context(), sqlc.RouteAwakeMinutesParams{AccountID: accountID, AppID: appID, SelectedScope: tc.scope, SelectedEnvironmentID: tc.env, SinceAt: since, UntilAt: until})
			if err != nil || len(usage) != 1 || usage[0].MbSeconds != tc.mb {
				t.Fatalf("scoped usage=%+v err=%v", usage, err)
			}
			routes, err := s.RouteAwakeRouteMinutes(t.Context(), sqlc.RouteAwakeRouteMinutesParams{AccountID: accountID, AppID: appID, SelectedScope: tc.scope, SelectedEnvironmentID: tc.env, SinceAt: since, UntilAt: until, RouteLimit: 10})
			if err != nil || len(routes) != 1 || routes[0].Requests != tc.requests || routes[0].Route != "GET /shared" {
				t.Fatalf("scoped routes=%+v err=%v", routes, err)
			}
		})
	}
	rows, err := s.RouteAdviceRouteStats(t.Context(), sqlc.RouteAdviceRouteStatsParams{AccountID: accountID, AppID: appID, SelectedScope: "staging", SelectedEnvironmentID: uuid.NewString(), SinceAt: since, UntilAt: until, RouteLimit: 10, CacheMaxAgeSeconds: 60})
	if err != nil || len(rows) != 0 {
		t.Fatalf("recreated environment inherited history: %+v %v", rows, err)
	}
}
