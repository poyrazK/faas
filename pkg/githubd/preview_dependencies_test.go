package githubd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

type previewDependencyEnqueuer struct{ specs []BuildSpec }

func (e *previewDependencyEnqueuer) Enqueue(_ context.Context, spec BuildSpec) (state.Build, error) {
	e.specs = append(e.specs, spec)
	return state.Build{ID: fmt.Sprintf("build-%d", len(e.specs))}, nil
}

func TestHandlePullRequest_ProvisionsOnlyTransitiveDependencies(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	if err := rig.mem.UpdateAccountPlan(ctx, rig.acct, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker", "db", "metrics"} {
		if _, err := rig.mem.CreateApp(ctx, state.App{
			AccountID: rig.acct, ProjectID: rig.parentProjectID, Slug: name,
			WorkloadName: name, Type: state.AppTypeApp, RAMMB: 256,
			MaxConcurrency: 1, Status: state.AppActive,
		}); err != nil {
			t.Fatalf("create production %s: %v", name, err)
		}
	}
	svc, _ := newPreviewService(t, rig)
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"compose.yaml":       &fstest.MapFile{Data: []byte("services: {}\n")},
		"Dockerfile.preview": &fstest.MapFile{Data: []byte("FROM scratch\n")},
	}}
	svc.WorkDir = t.TempDir()
	enqueuer := &previewDependencyEnqueuer{}
	svc.Enqueuer = enqueuer
	svc.Reconcile.Scan = func(fs.FS) (reposcan.Result, error) {
		return reposcan.Result{
			Workloads: []reposcan.Workload{
				{Name: "api", DependsOn: []string{"worker", "redis"}, Dockerfile: "Dockerfile.preview", Command: []string{"node", "api.js"}},
				{Name: "worker", DependsOn: []string{"db"}},
				{Name: "db"}, {Name: "metrics"},
			},
			Managed: []reposcan.Managed{{Name: "redis"}},
		}, nil
	}
	assertBuilds := func(start int, want []string, pr int) {
		t.Helper()
		if len(enqueuer.specs) != start+len(want) {
			t.Fatalf("enqueued %d builds, want %d", len(enqueuer.specs), start+len(want))
		}
		for i, name := range want {
			spec := enqueuer.specs[start+i]
			if spec.App.WorkloadName != name || spec.App.PreviewPrNumber != pr ||
				spec.EventKind != githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST ||
				spec.SourcePath == "" {
				t.Errorf("build %d = %+v, want PR #%d workload %q with staged source", start+i, spec, pr, name)
			}
		}
	}
	first, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if err != nil {
		t.Fatalf("open PR #42: %v", err)
	}
	if len(first.Added) != 3 || len(first.BuildIDs) != 3 {
		t.Fatalf("first result = %+v, want three preview apps and builds", first)
	}
	assertBuilds(0, []string{"db", "worker", "api"}, 42)
	set, err := rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42)
	if err != nil || set.CommitSHA != strings.Repeat("a", 40) || len(set.MemberAppIDs) != 3 || set.RootAppID != first.Added[0].ID {
		t.Fatalf("first preview revision set = (%+v, %v)", set, err)
	}
	for _, slug := range []string{"pr-42-db", "pr-42-worker", "pr-42-demo-app"} {
		if _, err := rig.mem.AppBySlug(ctx, slug); err != nil {
			t.Fatalf("missing preview %s: %v", slug, err)
		}
	}
	apiPreview, _ := rig.mem.AppBySlug(ctx, "pr-42-demo-app")
	workerPreview, _ := rig.mem.AppBySlug(ctx, "pr-42-worker")
	if apiPreview.Manifest.Env["GREGALE_SERVICE_WORKER_URL"] != "http://worker.svc.gregale:10080" ||
		apiPreview.Manifest.Env["GREGALE_SERVICE_REDIS_URL"] != "" ||
		workerPreview.Manifest.Env["GREGALE_SERVICE_DB_URL"] != "http://db.svc.gregale:10080" {
		t.Fatalf("PR-head service env not applied: api=%v worker=%v", apiPreview.Manifest.Env, workerPreview.Manifest.Env)
	}
	if apiPreview.Manifest.BuildDockerfile != "Dockerfile.preview" || apiPreview.StartCommand != "node api.js" {
		t.Fatalf("PR-head workload metadata not applied: dockerfile=%q command=%q", apiPreview.Manifest.BuildDockerfile, apiPreview.StartCommand)
	}
	productionAPI, _ := rig.mem.AppByID(ctx, rig.parentID)
	if productionAPI.Manifest.Env["GREGALE_SERVICE_WORKER_URL"] != "" {
		t.Fatal("PR head source mutated production app manifest")
	}
	if _, err := rig.mem.AppBySlug(ctx, "pr-42-metrics"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unrelated metrics preview = %v, want ErrNotFound", err)
	}

	second, err := svc.handlePullRequest(ctx, pullRequestSyncBody(42, strings.Repeat("b", 40)))
	if err != nil || len(second.BuildIDs) != 3 {
		t.Fatalf("synchronize PR #42 = (%+v, %v)", second, err)
	}
	assertBuilds(3, []string{"db", "worker", "api"}, 42)
	set, err = rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42)
	if err != nil || set.CommitSHA != strings.Repeat("b", 40) || len(set.MemberAppIDs) != 3 || set.Closed {
		t.Fatalf("synchronized preview revision set = (%+v, %v)", set, err)
	}
	for i := 0; i < 3; i++ {
		if enqueuer.specs[i].App.ID != enqueuer.specs[i+3].App.ID {
			t.Errorf("retry changed app ID for %s", enqueuer.specs[i].App.WorkloadName)
		}
	}
	if _, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(43, strings.Repeat("c", 40))); err != nil {
		t.Fatalf("open sibling PR #43: %v", err)
	}
	closed, err := svc.handlePullRequest(ctx, pullRequestClosedBody(42, strings.Repeat("b", 40)))
	if err != nil || len(closed.BuildIDs) != 0 {
		t.Fatalf("close PR #42 = (%+v, %v)", closed, err)
	}
	set, err = rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42)
	if err != nil || !set.Closed {
		t.Fatalf("closed preview revision set = (%+v, %v)", set, err)
	}
	for _, pr := range []int{42, 43} {
		for _, parentSlug := range []string{"db", "worker", "demo-app"} {
			preview, err := rig.mem.AppBySlug(ctx, fmt.Sprintf("pr-%d-%s", pr, parentSlug))
			if err != nil {
				t.Fatal(err)
			}
			want := state.PreviewPrStateClosed
			if pr == 43 {
				want = state.PreviewPrStateOpen
			}
			if preview.PreviewPrState != want {
				t.Errorf("PR #%d %s state = %q, want %q", pr, parentSlug, preview.PreviewPrState, want)
			}
		}
	}
}

func TestHandlePullRequest_CloseSiblingsWhenBoundPreviewIsGone(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t)
	for _, pr := range []int{42, 43} {
		if _, err := rig.mem.CreateApp(ctx, state.App{
			AccountID: rig.acct, ProjectID: rig.parentProjectID,
			Slug: fmt.Sprintf("pr-%d-worker", pr), WorkloadName: "worker",
			Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1,
			Status: state.AppActive, PreviewOfSlug: "worker",
			PreviewPrNumber: pr, PreviewPrState: state.PreviewPrStateOpen,
		}); err != nil {
			t.Fatal(err)
		}
	}
	svc, _ := newPreviewService(t, rig)
	if _, err := svc.handlePullRequest(ctx, pullRequestClosedBody(42, strings.Repeat("a", 40))); err != nil {
		t.Fatalf("close PR #42 without bound preview: %v", err)
	}
	for _, tc := range []struct {
		pr   int
		want string
	}{{42, state.PreviewPrStateClosed}, {43, state.PreviewPrStateOpen}} {
		app, err := rig.mem.AppBySlug(ctx, fmt.Sprintf("pr-%d-worker", tc.pr))
		if err != nil || app.PreviewPrState != tc.want {
			t.Errorf("PR #%d sibling = (%+v, %v), want state %q", tc.pr, app, err, tc.want)
		}
	}
}

func TestHandlePullRequest_DependencyQuotaStopsBuilds(t *testing.T) {
	ctx := context.Background()
	rig := newPreviewRig(t) // Hobby permits five total apps.
	for _, name := range []string{"worker", "db"} {
		if _, err := rig.mem.CreateApp(ctx, state.App{
			AccountID: rig.acct, ProjectID: rig.parentProjectID, Slug: name,
			WorkloadName: name, Type: state.AppTypeApp, RAMMB: 256,
			MaxConcurrency: 1, Status: state.AppActive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	svc, rec := newPreviewService(t, rig)
	svc.Source = &stubSource{fsys: fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte("services: {}\n")}}}
	svc.WorkDir = t.TempDir()
	enqueuer := &previewDependencyEnqueuer{}
	svc.Enqueuer = enqueuer
	svc.Reconcile.Scan = func(fs.FS) (reposcan.Result, error) {
		return reposcan.Result{Workloads: []reposcan.Workload{
			{Name: "api", DependsOn: []string{"worker"}},
			{Name: "worker", DependsOn: []string{"db"}},
			{Name: "db"},
		}}, nil
	}
	result, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
	if !IsIgnored(err) || !result.WasIgnored || len(enqueuer.specs) != 0 {
		t.Fatalf("quota result = (%+v, %v), builds = %d; want ignored without builds", result, err, len(enqueuer.specs))
	}
	if _, err := rig.mem.GetPRPreviewSet(ctx, rig.install, "octo/api", 42); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("quota refusal recorded preview set: %v", err)
	}
	if len(rec.checks) != 1 || rec.checks[0].phase != githubdgrpc.CheckPhaseFailed {
		t.Fatalf("checks = %+v, want failed quota check", rec.checks)
	}
	for _, slug := range []string{"pr-42-demo-app", "pr-42-db", "pr-42-worker"} {
		if _, err := rig.mem.AppBySlug(ctx, slug); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("partial over-quota preview %q = %v, want ErrNotFound", slug, err)
		}
	}
}
