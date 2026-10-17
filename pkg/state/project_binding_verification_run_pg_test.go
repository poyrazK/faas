//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPgGraphVerificationAtomicCreationAdmissionAndFence(t *testing.T) {
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
	// Force the second member insert to fail after the parent and first child.
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_graph_worker_run() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.snapshot->'Report'->>'app'='graph-worker' THEN RAISE EXCEPTION 'fixture journal failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_graph_worker BEFORE INSERT ON binding_verification_runs FOR EACH ROW EXECUTE FUNCTION reject_graph_worker_run()`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProjectBindingVerificationRun(ctx, acct.ID, p.ID, in); err == nil {
		t.Fatal("forced member insertion unexpectedly succeeded")
	}
	var parents, children int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM project_binding_verification_runs),(SELECT count(*) FROM binding_verification_runs)`).Scan(&parents, &children); err != nil || parents != 0 || children != 0 {
		t.Fatalf("partial graph: %d %d %v", parents, children, err)
	}
	if _, err = pool.Exec(ctx, `DROP TRIGGER reject_graph_worker ON binding_verification_runs; DROP FUNCTION reject_graph_worker_run()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			out, err := s.CreateProjectBindingVerificationRun(ctx, acct.ID, p.ID, in)
			if err != nil || out.ID != id || !out.ExpiresAt.Equal(in.Report.ExpiresAt) {
				t.Errorf("replay: %+v %v", out, err)
			}
		})
	}
	wg.Wait()
	captures := map[string][]state.VerificationRunAdmission{}
	for name, child := range in.Members {
		captures[name] = child.Admissions
	}
	if err = s.CheckProjectBindingVerificationRun(ctx, acct.ID, p.ID, "production", id, captures); err != nil {
		t.Fatalf("batched review: %v", err)
	}
	captures["graph-worker"] = nil
	if err = s.CheckProjectBindingVerificationRun(ctx, acct.ID, p.ID, "production", id, captures); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("incomplete captured review accepted: %v", err)
	}
	first := in.Report.Members["graph-front"]
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			step, err := s.AdmitBindingVerificationRunStep(ctx, acct.ID, first.Policy.AppID, first.ID, 0)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- step.ResourceID
		})
	}
	wg.Wait()
	close(ids)
	resource := ""
	for current := range ids {
		if resource == "" {
			resource = current
		}
		if current != resource {
			t.Fatal("duplicate resources")
		}
	}
	if resource == "" {
		t.Fatal("no admitted resource")
	}
	var messages int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM invocations WHERE app_id=$1`, first.Policy.AppID).Scan(&messages); err != nil || messages != 1 {
		t.Fatalf("duplicate messages: %d %v", messages, err)
	}
	other := in.Report.Members["graph-worker"]
	if err = s.MarkAppRuntimeConfigChanged(ctx, other.Policy.AppID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdmitBindingVerificationRunStep(ctx, acct.ID, first.Policy.AppID, first.ID, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("other member drift bypassed: %v", err)
	}
	if err = s.SetProjectBindingVerificationRunStatus(ctx, acct.ID, p.ID, "production", id, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetBindingVerificationRunStatus(ctx, acct.ID, first.Policy.AppID, first.ID, "cancelled"); err != nil {
		t.Fatalf("cleanup blocked: %v", err)
	}
	if _, err = s.AdmitBindingVerificationRunStep(ctx, acct.ID, other.Policy.AppID, other.ID, 0); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("cancelled parent admitted: %v", err)
	}
	// The creation identity cannot authorize another reviewed graph.
	changed := in
	changed.Report.ReviewDigest = strings.Repeat("b", 64)
	if _, err = s.CreateProjectBindingVerificationRun(ctx, acct.ID, p.ID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("changed replay: %v", err)
	}
}
