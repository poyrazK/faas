package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBindingReleasePolicyGraphBackstopMem(t *testing.T) {
	bindingReleaseGraphSuite(t, state.NewMemStore())
}
func TestBindingReleasePolicyGraphBackstopPG(t *testing.T) {
	s, _ := pgStore(t)
	bindingReleaseGraphSuite(t, s)
}

func TestBindingReleasePolicyZeroStageCanaryMem(t *testing.T) {
	bindingReleaseZeroStageCanarySuite(t, state.NewMemStore())
}
func TestBindingReleasePolicyZeroStageCanaryPG(t *testing.T) {
	s, _ := pgStore(t)
	bindingReleaseZeroStageCanarySuite(t, s)
}
func bindingReleaseZeroStageCanarySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, serving, _ := bindingPromotionFixture(t, s)
	zero := int64(0)
	if _, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "default", ImageDigest: serving.ImageDigest, CanaryPreset: "custom", CanaryTotalSteps: 3, CanaryStages: []byte(`[{"percent":0,"duration":"1s"},{"percent":10,"duration":"1s"},{"percent":100,"duration":"0s"}]`), RolloutState: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, d.ID, "/candidate/image", "candidate/"+d.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	d, err = s.DeploymentByID(ctx, d.ID)
	if err != nil || d.Status != state.DeployLive || d.TrafficPercent != 0 || d.CanaryStep != 0 {
		t.Fatalf("zero stage changed: %+v %v", d, err)
	}
	stable, err := s.DeploymentByID(ctx, serving.ID)
	if err != nil || stable.TrafficPercent != 100 {
		t.Fatalf("stable changed: %+v %v", stable, err)
	}
}
func bindingReleaseGraphSuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateAccount(ctx, uuid.NewString()+"@release-graph.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "release-policy"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: project.ID, Slug: "release-member", Type: state.AppTypeApp, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:graph", TrafficPercent: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLive(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	graphs := s.(state.ProjectReleaseSetStore)
	members := []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: d.ID}}
	first, err := graphs.PublishProjectReleaseSet(ctx, a.ID, project.ID, "production", 1800, members)
	if err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if _, err := s.(state.BindingReleasePolicyStore).SetBindingReleasePolicy(ctx, a.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := graphs.PublishProjectReleaseSet(ctx, a.ID, project.ID, "production", 1800, members); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("graph bypass: %v", err)
	}
	active, _, err := graphs.ResolveProjectRelease(ctx, app.ID, "production", "")
	if err != nil || active != first.ID {
		t.Fatalf("graph mutated: %s %v", active, err)
	}
}

func TestBindingReleasePolicyPGLockRaces(t *testing.T) {
	for _, scenario := range []string{"policy changed", "expired", "enabled during unguarded write"} {
		t.Run(scenario, func(t *testing.T) {
			s, pool, _ := pgStoreWithPool(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			a, app, serving, candidate := bindingPromotionFixture(t, s)
			zero := int64(0)
			if scenario != "enabled during unguarded write" {
				if _, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
					t.Fatal(err)
				}
			}
			fence := bindingPromotionFence(t, s, a, app, candidate)
			fence.PolicyRevision = 1
			fence.MaxVerificationAge = api.DefaultBindingVerificationAge
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if scenario == "policy changed" {
				_, err = tx.Exec(ctx, `UPDATE app_binding_release_policies SET revision=revision+1,max_age_seconds=30 WHERE app_id=$1 AND scope='default'`, app.ID)
			} else if scenario == "enabled during unguarded write" {
				_, err = tx.Exec(ctx, `INSERT INTO app_binding_release_policies(app_id,scope,mode,revision,max_age_seconds) VALUES($1,'default','enforce',1,600)`, app.ID)
			} else {
				_, err = tx.Exec(ctx, `SELECT app_id FROM app_binding_promotion_revisions WHERE app_id=$1 FOR UPDATE`, app.ID)
				fence.ValidUntil = time.Now().Add(100 * time.Millisecond)
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				writeCtx := state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence})
				if scenario == "enabled during unguarded write" {
					writeCtx = ctx
				}
				_, err := s.UpdateDeploymentTraffic(writeCtx, candidate.ID, 25)
				done <- err
			}()
			for {
				var waiting bool
				// Lifecycle authorization takes account and app locks before the
				// binding-policy query, so policy writes may block either fence.
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND (query LIKE '%LockRoutePolicyAccount%' OR query LIKE '%LockRoutePolicyApp%' OR query LIKE '%LockDeploymentTrafficApp%' OR query LIKE '%AuthorizeBindingReleaseTraffic%' OR query LIKE '%update deployments set traffic_percent%'))`).Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				if waiting {
					break
				}
				select {
				case err := <-done:
					t.Fatalf("write did not wait: %v", err)
				default:
				}
				select {
				case <-ctx.Done():
					t.Fatal("traffic lock never observed")
				case <-time.After(10 * time.Millisecond):
				}
			}
			if scenario == "expired" {
				for time.Now().Before(fence.ValidUntil) {
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			want := state.ErrBindingPromotionChanged
			if scenario == "expired" {
				want = state.ErrBindingPromotionExpired
			}
			if scenario == "enabled during unguarded write" {
				want = state.ErrBindingReleaseRequired
			}
			if !errors.Is(err, want) {
				t.Fatalf("race admitted: %v want %v", err, want)
			}
			for id, percent := range map[string]int{candidate.ID: 0, serving.ID: 100} {
				d, err := s.DeploymentByID(ctx, id)
				if err != nil || d.TrafficPercent != percent {
					t.Fatalf("race traffic: %+v %v", d, err)
				}
			}
		})
	}
}

func TestBindingReleasePolicyMem(t *testing.T) { bindingReleasePolicySuite(t, state.NewMemStore()) }
func TestBindingReleasePolicyPG(t *testing.T)  { s, _ := pgStore(t); bindingReleasePolicySuite(t, s) }

func bindingReleasePolicySuite(t *testing.T, s state.Store) {
	t.Helper()
	ctx := context.Background()
	a, app, serving, candidate := bindingPromotionFixture(t, s)
	policies := s.(state.BindingReleasePolicyStore)
	p, err := policies.GetBindingReleasePolicy(ctx, a.ID, app.ID, "default")
	if err != nil || p.Mode != "off" || p.Revision != 0 {
		t.Fatalf("default: %+v %v", p, err)
	}
	zero := int64(0)
	p, err = policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero, MaxVerificationAge: "1m", RequireApplicationAck: true})
	if err != nil || p.Revision != 1 || p.MaxVerificationAge != "1m0s" {
		t.Fatalf("set: %+v %v", p, err)
	}
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero}); !errors.Is(err, state.ErrBindingReleasePolicyRevision) {
		t.Fatalf("lost CAS: %v", err)
	}
	if _, err := policies.GetBindingReleasePolicy(ctx, "00000000-0000-0000-0000-000000000001", app.ID, "default"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign policy: %v", err)
	}
	other, err := policies.GetBindingReleasePolicy(ctx, a.ID, app.ID, "staging")
	if err != nil || other.Mode != "off" {
		t.Fatalf("scope isolation: %+v %v", other, err)
	}
	assertWeights := func(want int) {
		t.Helper()
		for id, percent := range map[string]int{candidate.ID: want, serving.ID: 100 - want} {
			d, err := s.DeploymentByID(ctx, id)
			if err != nil || d.TrafficPercent != percent {
				t.Fatalf("traffic: %+v %v", d, err)
			}
		}
	}
	newDeployment := candidate
	newDeployment.ID = ""
	newDeployment.TrafficPercent = 100
	if _, err := s.CreateDeployment(ctx, newDeployment); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("positive candidate admission bypass: %v", err)
	}
	if _, err := s.UpdateDeploymentTraffic(ctx, candidate.ID, 25); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("unguarded traffic: %v", err)
	}
	assertWeights(0)
	gate := s.(state.BindingPromotionStore)
	fence := bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision = p.Revision
	fence.MaxVerificationAge = time.Minute
	fence.RequireApplicationAck = true
	for _, scenario := range []string{"unsupported waiver", "missing application ack", "looser age", "wrong policy revision"} {
		wrong := fence
		switch scenario {
		case "unsupported waiver":
			wrong.AllowUnsupported = true
		case "missing application ack":
			wrong.RequireApplicationAck = false
		case "looser age":
			wrong.MaxVerificationAge = 2 * time.Minute
		case "wrong policy revision":
			wrong.PolicyRevision = 0
		}
		if _, err := gate.PromoteDeploymentWithBindings(ctx, candidate.ID, wrong, serving.ID); !errors.Is(err, state.ErrBindingPromotionChanged) && !state.IsBindingReleaseRequired(err) {
			t.Fatalf("%s bypass: %v", scenario, err)
		}
		assertWeights(0)
	}
	_, err = s.UpdateDeploymentTraffic(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), candidate.ID, 25)
	if err != nil {
		t.Fatalf("checked partial traffic: %v", err)
	}
	assertWeights(25)
	// Reducing the target increases its sibling; that sibling needs evidence.
	fence = bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision = p.Revision
	fence.MaxVerificationAge = time.Minute
	fence.RequireApplicationAck = true
	if _, err := s.UpdateDeploymentTraffic(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), candidate.ID, 0); !state.IsBindingReleaseRequired(err) {
		t.Fatalf("sibling bypass: %v", err)
	}
	assertWeights(25)
	servingFence := bindingPromotionFence(t, gate, a, app, serving)
	servingFence.PolicyRevision = p.Revision
	servingFence.MaxVerificationAge = time.Minute
	servingFence.RequireApplicationAck = true
	if _, err := s.UpdateDeploymentTraffic(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{servingFence}), candidate.ID, 0); err != nil {
		t.Fatalf("checked recovery: %v", err)
	}
	assertWeights(0)
	fence = bindingPromotionFence(t, gate, a, app, candidate)
	fence.PolicyRevision = p.Revision
	fence.MaxVerificationAge = time.Minute
	fence.RequireApplicationAck = true
	revision := p.Revision
	p, err = policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &revision, MaxVerificationAge: "30s", RequireApplicationAck: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDeploymentTraffic(state.WithBindingReleaseFences(ctx, []state.BindingPromotionFence{fence}), candidate.ID, 100); !errors.Is(err, state.ErrBindingPromotionChanged) {
		t.Fatalf("policy race: %v", err)
	}
	assertWeights(0)
	revision = p.Revision
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "off", ExpectedRevision: &revision}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unaudited disable: %v", err)
	}
	if _, err := policies.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "off", ExpectedRevision: &revision, Reason: "incident recovery"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDeploymentTraffic(ctx, candidate.ID, 100); err != nil {
		t.Fatalf("emergency recovery: %v", err)
	}
	assertWeights(100)
}

func TestBindingReleasePolicyPGBackstopAndAudit(t *testing.T) {
	s, pool, _ := pgStoreWithPool(t)
	ctx := context.Background()
	a, app, serving, candidate := bindingPromotionFixture(t, s)
	zero := int64(0)
	_, err := s.SetBindingReleasePolicy(ctx, a.ID, app.ID, "default", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: &zero})
	if err != nil {
		t.Fatal(err)
	}
	// Direct SQL is representative of an unintegrated traffic writer.
	_, err = pool.Exec(ctx, "UPDATE deployments SET traffic_percent=50 WHERE id=$1", candidate.ID)
	if !state.IsBindingReleaseRequired(err) {
		t.Fatalf("SQL bypass: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM app_binding_release_policy_history WHERE app_id=$1", app.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("durable audit: %d %v", count, err)
	}
	// No policy means no change to the stable row or another scope.
	d, err := s.DeploymentByID(ctx, serving.ID)
	if err != nil || d.TrafficPercent != 100 {
		t.Fatalf("stable: %+v %v", d, err)
	}
}
