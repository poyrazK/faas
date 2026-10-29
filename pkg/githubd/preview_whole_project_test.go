package githubd

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261025093331: under the project service policy a preview may call
// any sibling without declaring it, so every callable sibling joins the
// preview. Workers and scheduled workloads join only as declared
// dependencies; a PR head that declares another policy keeps the closure.
func TestHandlePullRequest_ProjectPolicyPreviewsEveryCallableSibling(t *testing.T) {
	for _, tc := range []struct {
		name       string
		rootPolicy reposcan.ServiceBindingPolicy
		want       []string
		absent     []string
	}{
		{"project policy", "", []string{"billing", "ledger"}, []string{"worker", "report"}},
		{"PR head declares declared", reposcan.ServiceBindingPolicyDeclared, nil, []string{"billing", "ledger", "worker", "report"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			rig := newPreviewRig(t)
			if err := rig.mem.UpdateAccountPlan(ctx, rig.acct, api.PlanScale); err != nil {
				t.Fatal(err)
			}
			rootManifest := state.AppManifest{ServiceBindingPolicy: api.ServiceBindingPolicyProject}
			if _, err := rig.mem.UpdateApp(ctx, rig.parentID, state.UpdateAppParams{Manifest: &rootManifest}); err != nil {
				t.Fatal(err)
			}
			for name, class := range map[string]state.WorkloadClass{
				"billing": state.WorkloadClassHTTP, "ledger": state.WorkloadClassHTTP, "worker": state.WorkloadClassWorker, "report": state.WorkloadClassHTTP,
			} {
				if _, err := rig.mem.CreateApp(ctx, state.App{
					AccountID: rig.acct, ProjectID: rig.parentProjectID, Slug: name, WorkloadName: name,
					WorkloadClass: class, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, Status: state.AppActive,
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
						{Name: "api", ServiceBindingPolicy: tc.rootPolicy, Dockerfile: "Dockerfile.preview"},
						{Name: "billing", DependsOn: []string{"redis"}},
						{Name: "ledger"},
						// The production class marks worker as a queue consumer
						// even though the scan cannot tell.
						{Name: "worker"},
						{Name: "report", Schedule: "0 * * * *"},
					},
					Managed: []reposcan.Managed{{Name: "redis"}},
				}, nil
			}
			result, err := svc.handlePullRequest(ctx, pullRequestOpenedBody(42, strings.Repeat("a", 40)))
			if err != nil {
				t.Fatalf("open PR: %v", err)
			}
			if len(result.Added) != 1+len(tc.want) {
				t.Fatalf("added %d previews, want the root and %v", len(result.Added), tc.want)
			}
			for _, name := range tc.want {
				preview, err := rig.mem.AppBySlug(ctx, "pr-42-"+name)
				if err != nil || preview.PreviewPrNumber != 42 || preview.ProjectID != rig.parentProjectID {
					t.Fatalf("missing %s preview: (%+v, %v)", name, preview, err)
				}
			}
			for _, name := range tc.absent {
				if _, err := rig.mem.AppBySlug(ctx, "pr-42-"+name); !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("%s preview = %v, want none", name, err)
				}
			}
		})
	}
}
