package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"sync"
	"testing"
	"time"
)

func graphReceiptFixture(t *testing.T) (*MemStore, Project, GraphVerificationCreate) {
	t.Helper()
	m, app, _, first := verificationReceiptFixture(t)
	ctx := t.Context()
	project, err := m.CreateProject(ctx, Project{AccountID: app.AccountID, Slug: "graph-receipt"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateProjectEnvironment(ctx, ProjectEnvironment{AccountID: app.AccountID, ProjectID: project.ID, Slug: first.Report.Scope}); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	a := m.apps[app.ID]
	a.ProjectID = project.ID
	a.Manifest.RevisionPinTTLSeconds = 3600
	m.apps[a.ID] = a
	d := m.deployments[first.Report.DeploymentID]
	d.Scope = first.Report.Scope
	m.deployments[d.ID] = d
	m.mu.Unlock()
	worker, err := m.CreateApp(ctx, App{AccountID: app.AccountID, ProjectID: project.ID, Slug: "graph-worker", Type: AppTypeFunction, Manifest: AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: worker.ID, Scope: first.Report.Scope, ImageDigest: "sha256:worker"})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetDeploymentRootfs(ctx, dep.ID, "/rootfs", "rootfs-worker", 1); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if err = m.MarkAppRuntimeConfigChanged(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	policy, err := m.SetBindingReleasePolicy(ctx, app.AccountID, worker.ID, first.Report.Scope, api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", MaxVerificationAge: "5m"})
	if err != nil {
		t.Fatal(err)
	}
	stamp, _, err := m.AppRuntimeConfigChangedAt(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	second := VerificationRunCreate{Report: api.BindingVerificationRun{ID: uuid.NewString(), App: worker.Slug, DeploymentID: dep.ID, Scope: first.Report.Scope, Status: "pending", CreatedAt: first.Report.CreatedAt, ExpiresAt: first.Report.ExpiresAt, QueuePublish: true, Policy: policy, Inventory: api.AppBindingInventory{App: worker.Slug, Complete: true, Scope: first.Report.Scope, RequestedDeploymentID: dep.ID, VerificationDeploymentID: dep.ID, VerificationScope: first.Report.Scope, RuntimeFreshness: &api.BindingRuntimeFreshness{Source: "instance_started_at", ConfigChangedAt: &stamp}}, Steps: []api.BindingVerificationRunStep{}}, Admissions: []VerificationRunAdmission{}}
	second.Report.ReviewDigest = bindingcheck.VerificationReviewDigest(second.Report.Inventory, policy)
	empty := ""
	manifest := api.CheckedProjectReleaseManifest{Project: project.Slug, Environment: first.Report.Scope, TTLSeconds: 60, Deployments: map[string]string{app.Slug: stageCanonicalID(first.Report.DeploymentID), worker.Slug: stageCanonicalID(dep.ID)}, ExpectedActiveReleaseID: &empty, ExpectedFallbackDeployments: map[string]string{}, ExpectedPolicyRevisions: map[string]int64{app.Slug: first.Report.Policy.Revision, worker.Slug: policy.Revision}, ServiceTargets: map[string]map[string]string{app.Slug: {}, worker.Slug: {}}}
	for _, member := range []App{app, worker} {
		deps, err := m.LiveDeployments(ctx, member.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range deps {
			if d.Scope == manifest.Environment && d.TrafficPercent > 0 {
				manifest.ExpectedFallbackDeployments[member.Slug] = stageCanonicalID(d.ID)
			}
		}
	}
	id := uuid.NewString()
	first.Report.GraphRunID = id
	second.Report.GraphRunID = id
	digests := map[string]string{app.Slug: first.Report.ReviewDigest, worker.Slug: second.Report.ReviewDigest}
	report := api.ProjectBindingVerificationRun{ID: id, Project: project.Slug, Environment: first.Report.Scope, Status: "pending", CreatedAt: first.Report.CreatedAt, ExpiresAt: first.Report.ExpiresAt, Manifest: manifest, QueuePublish: true, ReviewDigest: api.ProjectBindingVerificationReviewDigest(api.CreateProjectBindingVerificationRunRequest{Manifest: manifest, ReviewDigests: digests, QueuePublish: true}), Members: map[string]api.BindingVerificationRun{app.Slug: first.Report, worker.Slug: second.Report}}
	return m, project, GraphVerificationCreate{Report: report, Members: map[string]VerificationRunCreate{app.Slug: first, worker.Slug: second}}
}
func TestGraphVerificationAtomicCreateReplayAndCancellation(t *testing.T) {
	m, p, in := graphReceiptFixture(t)
	ctx := t.Context()
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			out, err := m.CreateProjectBindingVerificationRun(ctx, p.AccountID, p.ID, in)
			if err != nil || out.ID != in.Report.ID || !out.ExpiresAt.Equal(in.Report.ExpiresAt) {
				t.Errorf("create replay: %+v %v", out, err)
			}
		})
	}
	wg.Wait()
	var child api.BindingVerificationRun
	for _, c := range in.Report.Members {
		if len(c.Steps) > 0 {
			child = c
		}
	}
	first, err := m.AdmitBindingVerificationRunStep(ctx, p.AccountID, child.Policy.AppID, child.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.AdmitBindingVerificationRunStep(ctx, p.AccountID, child.Policy.AppID, child.ID, 0)
	if err != nil || first.ResourceID != again.ResourceID {
		t.Fatalf("duplicate admission: %+v %v", again, err)
	}
	if err = m.SetProjectBindingVerificationRunStatus(ctx, p.AccountID, p.ID, in.Report.Environment, in.Report.ID, "cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.AdmitBindingVerificationRunStep(ctx, p.AccountID, child.Policy.AppID, child.ID, 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("parent cancellation did not fence child: %v", err)
	}
	if err = m.SetBindingVerificationRunStatus(ctx, p.AccountID, child.Policy.AppID, child.ID, "cancelled"); err != nil {
		t.Fatalf("cleanup fenced by parent: %v", err)
	}
	m.mu.Lock()
	graphs, runs := len(m.graphVerificationRuns), len(m.bindingVerificationRuns)
	m.mu.Unlock()
	if graphs != 1 || runs != 2 {
		t.Fatalf("duplicate receipts: %d %d", graphs, runs)
	}
}
func TestGraphVerificationOtherMemberDriftAndExpiryFenceAdmission(t *testing.T) {
	for _, change := range []string{"config", "membership", "targets", "expiry"} {
		t.Run(change, func(t *testing.T) {
			m, p, in := graphReceiptFixture(t)
			ctx := t.Context()
			if _, err := m.CreateProjectBindingVerificationRun(ctx, p.AccountID, p.ID, in); err != nil {
				t.Fatal(err)
			}
			var child, other api.BindingVerificationRun
			for _, c := range in.Report.Members {
				if len(c.Steps) > 0 {
					child = c
				} else {
					other = c
				}
			}
			switch change {
			case "config":
				if err := m.MarkAppRuntimeConfigChanged(ctx, other.Policy.AppID); err != nil {
					t.Fatal(err)
				}
			case "membership":
				m.mu.Lock()
				a := m.apps[other.Policy.AppID]
				a.ProjectID = ""
				m.apps[a.ID] = a
				m.mu.Unlock()
			case "targets":
				m.mu.Lock()
				a := m.apps[other.Policy.AppID]
				a.Manifest.ServiceBindings = []api.AppServiceBinding{{Service: child.App, Binding: "CHILD"}}
				m.apps[a.ID] = a
				m.mu.Unlock()
			case "expiry":
				m.mu.Lock()
				r := m.graphVerificationRuns[in.Report.ID]
				r.Report.ExpiresAt = time.Now().Add(-time.Second)
				m.graphVerificationRuns[in.Report.ID] = r
				m.mu.Unlock()
			}
			if _, err := m.AdmitBindingVerificationRunStep(ctx, p.AccountID, child.Policy.AppID, child.ID, 0); !errors.Is(err, ErrConflict) {
				t.Fatalf("%s admitted new work: %v", change, err)
			}
			due, err := m.QueuePeek(ctx, child.Policy.AppID, 10, "")
			if err != nil || len(due) != 0 {
				t.Fatalf("blocked graph published work: %d %v", len(due), err)
			}
		})
	}
}
func TestGraphVerificationAtomicCreateCapacityFailure(t *testing.T) {
	m, p, in := graphReceiptFixture(t)
	ctx := context.Background()
	var child VerificationRunCreate
	for _, c := range in.Members {
		if len(c.Report.Steps) == 0 {
			child = c
		}
	}
	child.Report.GraphRunID = ""
	for range api.BindingVerificationRunMaxActive {
		child.Report.ID = uuid.NewString()
		if _, err := m.CreateBindingVerificationRun(ctx, p.AccountID, child.Report.Policy.AppID, child); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.CreateProjectBindingVerificationRun(ctx, p.AccountID, p.ID, in); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("capacity error: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.graphVerificationRuns) != 0 || len(m.bindingVerificationRuns) != api.BindingVerificationRunMaxActive {
		t.Fatal("partial graph/member receipts survived failed creation")
	}
}
