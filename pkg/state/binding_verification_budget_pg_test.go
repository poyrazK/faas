//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"sync"
	"testing"
	"time"
)

func pgVerificationBudgetFixture(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context, state.Account, state.Project, state.GraphVerificationCreate) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "graph-pg@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "graph-pg"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: p.ID, Slug: "production"}); err != nil && !errors.Is(err, state.ErrConflict) {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	id := uuid.NewString()
	empty := ""
	manifest := api.CheckedProjectReleaseManifest{Project: p.Slug, Environment: "production", TTLSeconds: 60, Deployments: map[string]string{}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{}, ServiceTargets: map[string]map[string]string{}}
	in := state.GraphVerificationCreate{Members: map[string]state.VerificationRunCreate{}, Report: api.ProjectBindingVerificationRun{ID: id, Project: p.Slug, Environment: "production", Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Minute), QueuePublish: true, Members: map[string]api.BindingVerificationRun{}}}
	digests := map[string]string{}
	for _, slug := range []string{"graph-front", "graph-worker"} {
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: p.ID, Slug: slug, WorkloadName: slug, Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
		if err != nil {
			t.Fatal(err)
		}
		env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
		dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Scope: "production", ImageDigest: "sha256:graph", OverrideEnv: env})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "graph-rootfs", 1); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: acct.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1}); err != nil {
			t.Fatal(err)
		}
		if err = s.MarkAppRuntimeConfigChanged(ctx, app.ID); err != nil {
			t.Fatal(err)
		}
		stamp, _, err := s.AppRuntimeConfigChangedAt(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		policy, err := s.SetBindingReleasePolicy(ctx, acct.ID, app.ID, "production", api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}})
		if err != nil {
			t.Fatal(err)
		}
		scope := "production"
		inventory := api.AppBindingInventory{App: slug, Scope: scope, Complete: true, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: scope, RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ConfigChangedAt: &stamp}, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: scope, QueueEnvironment: &scope, State: "enabled", Access: "push", ConsumerState: "active", ConsumerLiveness: "healthy", QueueCanarySupported: true}}}
		child := api.BindingVerificationRun{ID: uuid.NewString(), GraphRunID: id, App: slug, DeploymentID: dep.ID, Scope: scope, Status: "pending", CreatedAt: now, ExpiresAt: in.Report.ExpiresAt, QueuePublish: true, Policy: policy, Inventory: inventory, ReviewDigest: bindingcheck.VerificationReviewDigest(inventory, policy), Steps: []api.BindingVerificationRunStep{{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeQueue, Name: "orders", Binding: "orders", Scope: scope, QueueEnvironment: &scope, Action: "queue-canary", RequiredEvidence: api.QueueEvidenceHandlerCompletion}, Status: "planned"}}}
		in.Members[slug] = state.VerificationRunCreate{Report: child, Admissions: make([]state.VerificationRunAdmission, 1)}
		in.Report.Members[slug] = child
		digests[slug] = child.ReviewDigest
		manifest.Deployments[slug] = dep.ID
		manifest.ExpectedPolicyRevisions[slug] = policy.Revision
		manifest.ServiceTargets[slug] = map[string]string{}
		deps, err := s.LiveDeployments(ctx, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range deps {
			if d.TrafficPercent > 0 {
				manifest.ExpectedFallbackDeployments[slug] = d.ID
			}
		}
	}
	in.Report.Manifest = manifest
	in.Report.ReviewDigest = api.ProjectBindingVerificationReviewDigest(api.CreateProjectBindingVerificationRunRequest{Manifest: manifest, ReviewDigests: digests, QueuePublish: true})
	return s, pool, ctx, acct, p, in
}
func TestPgVerificationBudgetConcurrentGraphDrivers(t *testing.T) {
	s, pool, ctx, account, project, in := pgVerificationBudgetFixture(t)
	in.Report.MaxConcurrency = 1
	for name, member := range in.Members {
		member.Report.MaxConcurrency = 1
		in.Members[name] = member
		in.Report.Members[name] = member.Report
	}
	run, err := s.CreateProjectBindingVerificationRun(ctx, account.ID, project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	identities := map[string]string{}
	fullCount := 0
	for range 12 {
		for _, child := range run.Members {
			wg.Go(func() {
				step, err := s.AdmitBindingVerificationRunStep(ctx, account.ID, child.Policy.AppID, child.ID, 0)
				mu.Lock()
				defer mu.Unlock()
				var full *state.VerificationRunBudgetError
				if errors.As(err, &full) {
					fullCount++
					if full.Limit != 1 || full.Observed != 1 {
						t.Errorf("budget %+v", full)
					}
					return
				}
				if err != nil {
					t.Error(err)
					return
				}
				if old := identities[child.App]; old != "" && old != step.ResourceID {
					t.Error("duplicate admission")
				}
				identities[child.App] = step.ResourceID
			})
		}
	}
	wg.Wait()
	if len(identities) != 1 || fullCount != 12 {
		t.Fatalf("identities=%v blocked=%d", identities, fullCount)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invocations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("messages=%d %v", count, err)
	}
	var winner, waiting api.BindingVerificationRun
	var resource string
	for name, child := range run.Members {
		if id := identities[name]; id != "" {
			winner, resource = child, id
		} else {
			waiting = child
		}
	}
	budget, err := s.BindingVerificationRunBudget(ctx, account.ID, waiting.Policy.AppID, waiting.ID)
	if err != nil || budget.RunID != run.ID || !budget.Full || budget.ActiveResources != 1 || budget.WaitingSteps != 1 {
		t.Fatalf("shared budget %+v %v", budget, err)
	}
	changed := in
	changed.Report.MaxConcurrency = 2
	for name, child := range changed.Members {
		child.Report.MaxConcurrency = 2
		changed.Members[name] = child
		changed.Report.Members[name] = child.Report
	}
	if _, err = s.CreateProjectBindingVerificationRun(ctx, account.ID, project.ID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("budget mutation: %v", err)
	}
	if _, err = s.CancelQueueBindingCanary(ctx, account.ID, winner.Policy.AppID, resource); err != nil {
		t.Fatal(err)
	}
	second, err := s.AdmitBindingVerificationRunStep(ctx, account.ID, waiting.Policy.AppID, waiting.ID, 0)
	if err != nil || second.ResourceID == "" {
		t.Fatalf("slot did not release: %+v %v", second, err)
	}
	original, err := s.GetProjectBindingVerificationRun(ctx, account.ID, project.ID, run.Environment, run.ID)
	if err != nil || !original.ExpiresAt.Equal(run.ExpiresAt) {
		t.Fatalf("deadline changed: %+v %v", original, err)
	}
	if err = s.SetProjectBindingVerificationRunStatus(ctx, account.ID, project.ID, run.Environment, run.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitBindingVerificationRunStep(ctx, account.ID, waiting.Policy.AppID, waiting.ID, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancelled graph admitted: %v", err)
	}
}

func TestPgVerificationBudgetConcurrentAppDrivers(t *testing.T) {
	s, pool, ctx, account, _, graph := pgVerificationBudgetFixture(t)
	in := graph.Members["graph-front"]
	in.Report.GraphRunID = ""
	in.Report.MaxConcurrency = 1
	for range 7 {
		in.Report.Steps = append(in.Report.Steps, in.Report.Steps[0])
		in.Admissions = append(in.Admissions, in.Admissions[0])
	}
	run, err := s.CreateBindingVerificationRun(ctx, account.ID, in.Report.Policy.AppID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winner, resource, blocked := -1, "", 0
	for index := range run.Steps {
		wg.Go(func() {
			step, err := s.AdmitBindingVerificationRunStep(ctx, account.ID, run.Policy.AppID, run.ID, index)
			mu.Lock()
			defer mu.Unlock()
			var full *state.VerificationRunBudgetError
			if errors.As(err, &full) {
				blocked++
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			if resource != "" {
				t.Error("exceeded shared app budget")
			}
			winner, resource = index, step.ResourceID
		})
	}
	wg.Wait()
	if winner < 0 || blocked != 7 {
		t.Fatalf("winner=%d blocked=%d", winner, blocked)
	}
	replay, err := s.AdmitBindingVerificationRunStep(ctx, account.ID, run.Policy.AppID, run.ID, winner)
	if err != nil || replay.ResourceID != resource {
		t.Fatalf("replay at capacity: %+v %v", replay, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invocations`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("messages=%d %v", count, err)
	}
	if _, err = s.CancelQueueBindingCanary(ctx, account.ID, run.Policy.AppID, resource); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitBindingVerificationRunStep(ctx, account.ID, run.Policy.AppID, run.ID, (winner+1)%len(run.Steps)); err != nil {
		t.Fatalf("terminal resource did not release slot: %v", err)
	}
}
