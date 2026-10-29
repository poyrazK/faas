//go:build !no_pg

package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Activation unholds a GitOps workload deployment and serves it, keeping its
// environment_workload_runtime. A frameless serving instance of that
// deployment must still fail and record its runtime receipt; only a held
// candidate's frameless instance is a qualification the guards keep. Before,
// a serving cold boot that timed out stayed cold_booting forever, and a
// successful one could not record its receipt (n2, 2026-10-29).
func TestPgActivatedWorkloadServingInstanceCanFailAndDeliver(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	account, err := s.CreateAccount(ctx, "serving-guard-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "serving-guard-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "serving-guard-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 2, IdleTimeoutS: 60, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	node := resolveDefaultLocal(t, ctx, s)
	for _, tc := range []struct {
		name    string
		held    bool
		status  string
		refused bool
	}{
		{name: "held candidate", held: true, status: "pending", refused: true},
		{name: "activated deployment", held: false, status: "live", refused: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployPending, Scope: "production",
				TrafficPercent: 0, TrafficPercentExplicit: true})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateColdBooting), 128, node, "")
			if err != nil {
				t.Fatal(err)
			}
			serving, err := s.CreateInstance(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			// Seed the GitOps runtime and hold state without the candidate
			// transition triggers; the instance write below still meets the guard.
			if _, err := pool.Exec(ctx, `ALTER TABLE deployments DISABLE TRIGGER USER`); err != nil {
				t.Fatal(err)
			}
			_, seedErr := pool.Exec(ctx, `UPDATE deployments SET environment_workload_runtime=jsonb_build_object(
				'source_id',$1::text,'environment_id',$1::text,'revision_id',$1::text,
				'generation',1,'intent_version',1,'resource','workload/serving',
				'plan_hash',repeat('a',64),'app_id',$2::text,'scope','production',
				'baseline','{}'::jsonb,'start_command','[]'::jsonb,'runtime','{}'::jsonb),
				environment_workload_held=$3, status=$4 WHERE id=$1`, dep.ID, app.ID, tc.held, tc.status)
			_, enableErr := pool.Exec(ctx, `ALTER TABLE deployments ENABLE TRIGGER USER`)
			if seedErr != nil || enableErr != nil {
				t.Fatal(seedErr, enableErr)
			}
			err = s.UpdateInstanceStateToTerminal(ctx, instance.ID, string(state.StateFailed), time.Now().UTC())
			refused := err != nil && strings.Contains(err.Error(), "legacy qualification")
			if refused != tc.refused || !tc.refused && err != nil {
				t.Fatalf("fail a frameless instance (held=%v): err=%v, want refused=%v", tc.held, err, tc.refused)
			}
			// A serving wake records its delivered configuration; only a
			// candidate's instance must prove a current qualification attempt.
			err = s.RecordInstanceRuntimeConfigReceipt(ctx, serving.ID, serving.WakeID,
				state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC().Truncate(time.Microsecond)})
			refused = err != nil && strings.Contains(err.Error(), "current qualification attempt")
			if refused != tc.refused || !tc.refused && err != nil {
				t.Fatalf("serving runtime receipt (held=%v): err=%v, want refused=%v", tc.held, err, tc.refused)
			}
		})
	}
}
