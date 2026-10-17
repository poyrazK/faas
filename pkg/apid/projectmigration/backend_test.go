package projectmigration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func fixture(t *testing.T) (Backend, *state.MemStore, api.CreateProjectMigrationRequest, []state.App) {
	t.Helper()
	m := state.NewMemStore()
	ctx := t.Context()
	a, err := m.CreateAccount(ctx, "project-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	apps := []state.App{}
	for _, name := range []string{"api", "web"} {
		app, e := m.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: p.ID, Slug: "shop-" + name, WorkloadName: name, Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassHTTP})
		if e != nil {
			t.Fatal(e)
		}
		apps = append(apps, app)
	}
	if err := m.UpsertAppSecret(ctx, a.ID, apps[0].ID, "DATABASE_URL", []byte("sealed-test-input")); err != nil {
		t.Fatal(err)
	}
	b := Backend{Store: m, Reader: m, AccountID: a.ID, DigestKey: []byte("private-digest-key")}
	in := api.CreateProjectMigrationRequest{ID: uuid.NewString(), PlanToken: "private-source-token", Only: []string{"api", "web"}, InitialSecrets: api.ProjectInitialSecrets{"api": {"DATABASE_URL": "secret-value-never-persisted"}}}
	_, err = b.Create(ctx, in, api.PlanResponse{CanApply: true, Workloads: []api.PlanWorkload{{Name: "api", AppSlug: "shop-api", Class: "http", RequiredEnvKeys: []string{"DATABASE_URL"}}, {Name: "web", AppSlug: "shop-web", Class: "graphql", DependsOn: []string{"api"}}}}, "shop", "source-hash")
	if err != nil {
		t.Fatal(err)
	}
	return b, m, in, apps
}
func TestJournalRecoveryTargetsOnlyUnacceptedOrFailedServices(t *testing.T) {
	b, m, in, apps := fixture(t)
	ctx := t.Context()
	run, err := b.Begin(ctx, in.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.Prepare(ctx, apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	r, err := m.GetProjectMigration(ctx, b.AccountID, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range r.Report.Services {
		if _, err = m.CreateDeployment(ctx, state.Deployment{ID: s.DeploymentID, AppID: s.AppID, Kind: state.DeploymentKindTarball, Scope: api.DefaultEnvScope, Status: state.DeployPending}); err != nil {
			t.Fatal(err)
		}
		if _, err = m.CreateBuildWithID(ctx, s.BuildID, s.DeploymentID, state.DeploymentKindTarball, 0, ""); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err = m.SetDeploymentFailed(ctx, s.DeploymentID, "test", "failed"); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Simulate APID disappearing after queue acceptance but before Finish.
	r.LeaseUntil = time.Now().Add(-time.Second)
	if _, err = m.SaveProjectMigration(ctx, r, r.Version); err != nil {
		t.Fatal(err)
	}
	restarted := Backend{Store: m, Reader: m, AccountID: b.AccountID, DigestKey: b.DigestKey}
	out, err := restarted.Get(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Services[0].Status != "failed" || out.Services[1].Status != "building" {
		t.Fatalf("lost exact progress: %+v", out.Services)
	}
	if _, err = restarted.Begin(ctx, in.ID, "different-source", "shop", nil); err == nil {
		t.Fatal("changed source accepted")
	}
	if _, err = restarted.Begin(ctx, in.ID, "source-hash", "shop", api.ProjectInitialSecrets{"web": {"KEY": "do-not-rewrite"}}); err == nil {
		t.Fatal("accepted service configuration rewritten")
	}
	retry, err := restarted.Begin(ctx, in.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if names := retry.Names(); len(names) != 1 || names[0] != "api" {
		t.Fatalf("retry touched healthy service: %v", names)
	}
	if _, err = retry.Prepare(ctx, apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	after, err := m.GetProjectMigration(ctx, b.AccountID, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Report.Services[0].DeploymentID == r.Report.Services[0].DeploymentID || after.Report.Services[0].Attempt != 2 || after.Report.Services[1].DeploymentID != r.Report.Services[1].DeploymentID {
		t.Fatal("attempt identities or successful deployment changed")
	}
}

func TestJournalPinsAppNamesBeforeAppsArePrepared(t *testing.T) {
	b, m, in, _ := fixture(t)
	r, err := m.GetProjectMigration(t.Context(), b.AccountID, in.ID)
	if err != nil || r.Report.Services[0].App != "shop-api" || r.Report.Services[0].AppID != "" {
		t.Fatalf("mapping not saved before prepare: %+v %v", r, err)
	}
	if err = b.CheckAppSlugs(t.Context(), in.ID, map[string]string{"api": "replacement"}, []string{"api"}); err == nil {
		t.Fatal("changed allocation admitted")
	}
	after, err := m.GetProjectMigration(t.Context(), b.AccountID, in.ID)
	if err != nil || after.Version != r.Version || !after.LeaseUntil.IsZero() {
		t.Fatal("invalid name review mutated migration")
	}
	run, err := b.Begin(t.Context(), in.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = run.ValidateAppSlugs(map[string]string{"api": "replacement", "web": "shop-web"}); err == nil {
		t.Fatal("changed allocation accepted before first build")
	}
	if err = run.ValidateAppSlugs(map[string]string{"api": "shop-api", "web": "shop-web"}); err != nil {
		t.Fatal(err)
	}
}
func TestJournalReplayIsolationSecretsAndLease(t *testing.T) {
	b, m, in, _ := fixture(t)
	ctx := t.Context()
	r, err := m.GetProjectMigration(ctx, b.AccountID, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), "secret-value") || strings.Contains(string(raw), in.PlanToken) {
		t.Fatal("sensitive request material persisted")
	}
	if _, exists, err := b.Existing(ctx, in); err != nil || !exists {
		t.Fatal("lost create replay")
	}
	in.InitialSecrets["api"]["DATABASE_URL"] = "different-value"
	if _, _, err = b.Existing(ctx, in); !errors.Is(err, state.ErrConflict) {
		t.Fatal("changed create replay accepted")
	}
	other := b
	other.AccountID = uuid.NewString()
	if _, err = other.Get(ctx, in.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign migration readable")
	}
	var won atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, e := b.Begin(ctx, in.ID, "source-hash", "shop", nil); e == nil {
				won.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatalf("parallel apply claims=%d", won.Load())
	}
}
func TestJournalRejectsWorkersAndImageWorkloads(t *testing.T) {
	for _, w := range []api.PlanWorkload{{Name: "worker", Class: "worker"}, {Name: "cron", Class: "cron"}, {Name: "http", Class: "http", Image: "nginx:latest"}} {
		in := api.CreateProjectMigrationRequest{ID: uuid.NewString(), Only: []string{w.Name}}
		if ValidateSelection(in, api.PlanResponse{CanApply: true, Workloads: []api.PlanWorkload{w}}) == nil {
			t.Fatalf("unsupported workload accepted: %s", w.Name)
		}
	}
}

func TestJournalPollDuringRetryReservationAndRejectInvalidKeyNames(t *testing.T) {
	b, m, in, apps := fixture(t)
	ctx := t.Context()
	r, err := m.GetProjectMigration(ctx, b.AccountID, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.Report.ProjectID = apps[0].ProjectID
	r.Report.Services[0].AppID = apps[0].ID
	r.Report.Services[0].App = apps[0].Slug
	r.Report.Services[0].VerificationID = "old-deployment-verification"
	if _, err = m.SaveProjectMigration(ctx, r, r.Version); err != nil {
		t.Fatal(err)
	}
	b.Reader = guardedReader{reader: m, t: t}
	out, err := b.Get(ctx, in.ID)
	if err != nil || out.Services[0].Status != "pending" || out.Services[0].VerificationID != "" {
		t.Fatalf("reservation poll: %+v %v", out, err)
	}
	bad := in
	bad.ID = uuid.NewString()
	bad.InitialSecrets = api.ProjectInitialSecrets{"api": {"INVALID=key": "request-only"}}
	if _, err = b.Create(ctx, bad, api.PlanResponse{CanApply: true, Workloads: []api.PlanWorkload{{Name: "api", Class: "http"}, {Name: "web", Class: "http"}}}, "shop", "source-hash"); err == nil {
		t.Fatal("invalid key persisted")
	}
	if _, err = m.GetProjectMigration(ctx, b.AccountID, bad.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("invalid configuration entered journal")
	}
}

type guardedReader struct {
	reader   Reader
	t        *testing.T
	override *state.App
}

func (g guardedReader) AppByID(ctx context.Context, id string) (state.App, error) {
	if g.override != nil && g.override.ID == id {
		return *g.override, nil
	}
	return g.reader.AppByID(ctx, id)
}
func (g guardedReader) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	if !api.ValidProjectMigrationID(id) {
		g.t.Fatal("invalid deployment identity reached persistent reader")
	}
	return g.reader.DeploymentByID(ctx, id)
}
func (g guardedReader) BuildByID(ctx context.Context, id string) (state.Build, error) {
	if !api.ValidProjectMigrationID(id) {
		g.t.Fatal("invalid build identity reached persistent reader")
	}
	return g.reader.BuildByID(ctx, id)
}
func (g guardedReader) LatestDeploymentForScope(ctx context.Context, app, scope string) (state.Deployment, error) {
	return g.reader.LatestDeploymentForScope(ctx, app, scope)
}

func TestJournalDoesNotOverwriteAnUnrelatedDeploymentAfterFailedAdmission(t *testing.T) {
	b, m, in, apps := fixture(t)
	ctx := t.Context()
	run, err := b.Begin(ctx, in.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.Prepare(ctx, apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	if err = run.Finish(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateDeployment(ctx, state.Deployment{AppID: apps[0].ID, Kind: state.DeploymentKindTarball, Scope: api.DefaultEnvScope, Status: state.DeployPending}); err != nil {
		t.Fatal(err)
	}
	out, err := b.Get(ctx, in.ID)
	if err != nil || out.Services[0].Status != "changed" {
		t.Fatalf("unrelated deployment was adopted: %+v %v", out, err)
	}
	if _, err = b.Begin(ctx, in.ID, "source-hash", "shop", nil); err == nil {
		t.Fatal("resume could overwrite unrelated deployment")
	}
}
func TestJournalRequiresEveryOriginallySuppliedConfigurationKey(t *testing.T) {
	b, m, in, apps := fixture(t)
	ctx := t.Context()
	r, err := m.GetProjectMigration(ctx, b.AccountID, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.Report.Services[1].ConfiguredKeys = []string{"OPTIONAL_BUT_ORIGINALLY_SUPPLIED"}
	if _, err = m.SaveProjectMigration(ctx, r, r.Version); err != nil {
		t.Fatal(err)
	}
	run, err := b.Begin(ctx, in.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.Prepare(ctx, apps[0].ProjectID, apps); err == nil {
		t.Fatal("resume silently dropped unsaved configuration")
	}
}

func TestReviewedResumeFencesReplayAndOtherNewFailures(t *testing.T) {
	b, m, in, apps := fixture(t)
	ctx := t.Context()
	first, err := b.Get(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := b.BeginReviewed(ctx, in.ID, "source-hash", "shop", nil, first.Version, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if names := run.Names(); len(names) != 1 || names[0] != "api" {
		t.Fatalf("resume expanded service set: %v", names)
	}
	if _, err = run.Prepare(ctx, apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	if err = run.Finish(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err = b.BeginReviewed(ctx, in.ID, "source-hash", "shop", nil, first.Version, []string{"api"}); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale resume request replayed")
	}
	rows, err := m.ListProjectMigrations(ctx, b.AccountID, "shop")
	if err != nil || len(rows) != 1 {
		t.Fatal("resume created another migration")
	}
}

func TestJournalRejectsRenamedProjectMemberBeforeResume(t *testing.T) {
	b, _, in, apps := fixture(t)
	ctx := t.Context()
	run, err := b.Begin(ctx, in.ID, "source-hash", "shop", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.Prepare(ctx, apps[0].ProjectID, apps); err != nil {
		t.Fatal(err)
	}
	if err = run.Finish(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	moved := apps[0]
	moved.WorkloadName = "renamed-service"
	b.Reader = guardedReader{reader: b.Reader, t: t, override: &moved}
	out, err := b.Get(ctx, in.ID)
	if err != nil || out.Services[0].Status != "changed" {
		t.Fatalf("renamed member adopted: %+v %v", out, err)
	}
	if _, err = b.BeginReviewed(ctx, in.ID, "source-hash", "shop", nil, out.Version, []string{"api"}); err == nil {
		t.Fatal("renamed member permitted intent writes")
	}
}
