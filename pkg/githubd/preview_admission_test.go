// adr: 20261108140000
package githubd

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reposcan"
)

func failedPreviewSummary(rec *previewRecorder) string {
	for _, check := range rec.checks {
		if check.phase == githubdgrpc.CheckPhaseFailed {
			return check.summary
		}
	}
	return ""
}

// A preview whose service reads a Compose database's address, with no value
// for previews, fails its check naming the variable instead of starting the
// service without it; a pr-preview value lets the next push build.
func TestHandlePullRequest_PreviewNeedsDatastoreValue(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	if err := rig.mem.UpdateAccountPlan(ctx, rig.acct, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	svc, rec := newPreviewService(t, rig)
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte("services:\n  api:\n    build: .\n    environment:\n      DATABASE_URL: postgres://app:secret@db:5432/app\n  db:\n    image: postgres:16\n")},
		"Dockerfile":   &fstest.MapFile{Data: []byte("FROM scratch\n")},
	}}
	svc.WorkDir = t.TempDir()
	enqueuer := &previewDependencyEnqueuer{}
	svc.Enqueuer = enqueuer
	svc.Reconcile.Scan = func(fs.FS, reposcan.ScanOptions) (reposcan.Result, error) {
		return reposcan.Result{
			Workloads: []reposcan.Workload{{Name: "api", Dockerfile: "Dockerfile"}},
			Managed:   []reposcan.Managed{{Name: "db", Kind: "postgres", EnvHint: "DATABASE_URL"}},
		}, nil
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40))); !IsIgnored(err) {
		t.Fatalf("open PR = %v, want a reported setup failure", err)
	}
	if len(enqueuer.specs) != 0 {
		t.Fatalf("builds = %d, want none without a database value", len(enqueuer.specs))
	}
	summary := failedPreviewSummary(rec)
	if !strings.Contains(summary, "DATABASE_URL") || !strings.Contains(summary, "--app demo-app --env pr-preview") {
		t.Fatalf("failed check = %q, want the variable and the command", summary)
	}
	if err := rig.mem.UpsertAppEnvInScope(ctx, rig.acct, rig.parentID, api.PreviewEnvScope, "DATABASE_URL", "postgres://test@example.com/app"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40))); err != nil {
		t.Fatalf("PR with a pr-preview value = %v", err)
	}
	if len(enqueuer.specs) != 1 {
		t.Fatalf("builds = %d, want the api preview", len(enqueuer.specs))
	}
}

// A production database with previews on gives each preview its own
// DATABASE_URL (ADR-20261102123523), so the check must not refuse a preview
// for lacking a pr-preview value the preview database provides.
func TestHandlePullRequest_PreviewDatabaseProvidesDatastoreValue(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	if err := rig.mem.UpdateAccountPlan(ctx, rig.acct, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	svc, rec := newPreviewService(t, rig)
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte("services:\n  api:\n    build: .\n    environment:\n      DATABASE_URL: postgres://app:secret@db:5432/app\n  db:\n    image: postgres:16\n")},
		"Dockerfile":   &fstest.MapFile{Data: []byte("FROM scratch\n")},
	}}
	svc.WorkDir = t.TempDir()
	enqueuer := &previewDependencyEnqueuer{}
	svc.Enqueuer = enqueuer
	svc.Reconcile.Scan = func(fs.FS, reposcan.ScanOptions) (reposcan.Result, error) {
		return reposcan.Result{
			Workloads: []reposcan.Workload{{Name: "api", Dockerfile: "Dockerfile"}},
			Managed:   []reposcan.Managed{{Name: "db", Kind: "postgres", EnvHint: "DATABASE_URL"}},
		}, nil
	}
	lookups := 0
	svc.PreviewDatabaseEnvKeys = func(_ context.Context, accountID, appID string) ([]string, error) {
		lookups++
		if accountID != rig.acct || appID != rig.parentID {
			t.Errorf("lookup for %s/%s, want the production app", accountID, appID)
		}
		return []string{"DATABASE_URL"}, nil
	}
	allowed := false
	svc.CapabilityAllowed = func(capability, _ string) bool {
		return capability != PreviewDatabasesCapability || allowed
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40))); !IsIgnored(err) {
		t.Fatalf("open PR with preview databases off = %v, want a reported setup failure", err)
	}
	if summary := failedPreviewSummary(rec); !strings.Contains(summary, "DATABASE_URL") || strings.Contains(summary, "postgres previews") || lookups != 0 {
		t.Fatalf("failed check = %q (lookups %d), want the old refusal without the preview database hint", summary, lookups)
	}
	allowed = true
	if _, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40))); err != nil {
		t.Fatalf("PR whose database has previews on = %v", err)
	}
	if len(enqueuer.specs) != 1 || lookups != 1 {
		t.Fatalf("builds = %d, lookups = %d, want the api preview after one lookup", len(enqueuer.specs), lookups)
	}
}

// A preview whose builds do not all fit the hour's PR preview deploys builds
// none of them, so it never ends up half on the new commit.
func TestHandlePullRequest_PreviewOverDeployShareBuildsNothing(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	if err := rig.mem.UpdateAccountPlan(ctx, rig.acct, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	limit := api.PlanPro.PreviewDeploysPerHour()
	for range limit - 1 {
		if _, err := rig.mem.ConsumeAccountDeployRate(ctx, rig.acct, limit, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	svc, rec := newPreviewService(t, rig)
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"compose.yaml":       &fstest.MapFile{Data: []byte("services: {}\n")},
		"Dockerfile.preview": &fstest.MapFile{Data: []byte("FROM scratch\n")},
	}}
	svc.WorkDir = t.TempDir()
	enqueuer := &previewDependencyEnqueuer{}
	svc.Enqueuer = enqueuer
	svc.Reconcile.Scan = func(fs.FS, reposcan.ScanOptions) (reposcan.Result, error) {
		return reposcan.Result{Workloads: []reposcan.Workload{
			{Name: "api", DependsOn: []string{"worker"}, Dockerfile: "Dockerfile.preview"},
			{Name: "worker", Class: reposcan.ClassWorker, Command: []string{"node", "worker.js"}, Dockerfile: "Dockerfile.preview"},
		}}, nil
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40))); !IsIgnored(err) {
		t.Fatalf("open PR = %v, want a reported setup failure", err)
	}
	if len(enqueuer.specs) != 0 {
		t.Fatalf("builds = %d, want none", len(enqueuer.specs))
	}
	if summary := failedPreviewSummary(rec); !strings.Contains(summary, "needs 2 deploys") || !strings.Contains(summary, "1 of its") {
		t.Fatalf("failed check = %q, want the deploy shortfall", summary)
	}
}
