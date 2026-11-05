// adr: 570
package state

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func testTrafficAppBatchWithdrawal(t *testing.T, store Store, account Account, mode string, seed func(EdgeRule), intent func() string) {
	t.Helper()
	ctx := t.Context()
	project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "binding-batch", ScanSource: ProjectScanSourceSingle})
	if err != nil {
		t.Fatal(err)
	}
	app := App{AccountID: account.ID, ProjectID: project.ID, Slug: "binding-batch-web", WorkloadName: "web", WorkloadClass: WorkloadClassHTTP, RAMMB: 128, Status: AppActive}
	head := PRPreviewHead{InstallationID: 7, RepoFullName: "example/binding-batch", PRNumber: 42, CommitSHA: strings.Repeat("a", 40)}
	limits := api.MustLimitsFor(api.PlanScale)
	expiry := time.Now().UTC().Add(time.Hour)
	if mode == "preview" {
		app.PreviewOfSlug, app.PreviewPrNumber, app.PreviewPrState, app.PreviewExpiresAt = "web", 42, PreviewPrStateOpen, &expiry
		rows, reserveErr := store.(PRPreviewSetBatchStore).ReservePRPreviewSet(ctx, head, []App{app}, limits)
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		app = rows[0]
	} else {
		app, err = store.CreateApp(ctx, app)
		if err != nil {
			t.Fatal(err)
		}
	}
	surface, host := newTrafficTenantClaim(t, store, account, app, "batch-withdrawal.example.test")
	if err := store.UpdateTenantSurfaceStatus(ctx, surface.ID, SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkTenantHostnameVerified(ctx, host.Hostname); err != nil {
		t.Fatal(err)
	}
	peerAccount, peer := trafficTenantTransitionPeer(t, store)
	if _, err := store.CreateCustomDomain(ctx, "*.example.test", peer.ID, "private-batch-token"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, "*.example.test"); err != nil {
		t.Fatal(err)
	}
	seedTrafficAppCleanup(t, store, account, app)
	legacy := publicationLegacyRule(peerAccount, peer, host.Hostname)
	seed(legacy)
	before := intent()
	mutate := func(c context.Context) error {
		if mode == "project" {
			result, err := store.(ProjectReconcileStore).ApplyProjectReconcile(c, project, []ProjectReconcileMutation{{Op: "remove", App: app}}, []ProjectReconcileCron{}, ProjectScanSourceCompose, limits)
			if err != nil && !reflect.DeepEqual(result, ProjectReconcileResult{}) {
				t.Fatal("refusal returned reconcile rows")
			}
			return err
		}
		next := app
		next.ID, next.Slug, next.WorkloadName, next.PreviewOfSlug = "", "binding-batch-worker", "worker", "worker"
		next.CreatedAt = time.Time{}
		next.PreviewExpiresAt = &expiry
		replacement := head
		replacement.CommitSHA = strings.Repeat("b", 40)
		result, err := store.(PRPreviewSetBatchStore).ReservePRPreviewSet(c, replacement, []App{next}, limits)
		if err != nil && len(result) != 0 {
			t.Fatal("refusal returned preview rows")
		}
		return err
	}
	err = mutate(ctx)
	requireTrafficTenantRefusal(t, err)
	var binding *TrafficPolicyBindingError
	if !errors.As(err, &binding) {
		t.Fatalf("batch refusal lacks private binding marker: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := mutate(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled batch: %v", err)
	}
	if before != intent() {
		t.Fatal("refused batch changed apps, crons, cleanup, project, leases or preview receipt")
	}
	if mode == "project" {
		t.Run("final_topology", func(t *testing.T) {
			// Removing the tenant app alone is unsafe. Restoring that same
			// workload within the batch leaves the original binding selected.
			result, err := store.(ProjectReconcileStore).ApplyProjectReconcile(ctx, project,
				[]ProjectReconcileMutation{{Op: "remove", App: app}, {Op: "create", App: app}}, nil, "", limits)
			if err != nil || len(result.Added) != 1 || len(result.Removed) != 1 || result.Added[0].ID != app.ID || result.Added[0].Status != AppActive {
				t.Fatalf("safe final topology refused: %v", err)
			}
			current, err := store.AppByID(ctx, app.ID)
			if err != nil || current.Status != AppActive || current.DeletedAt != nil || current.DeleteGraceUntil != nil {
				t.Fatal("accepted final topology did not restore original app")
			}
		})
	}
	if err := store.DeleteEdgeRule(ctx, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := mutate(ctx); err != nil {
		t.Fatalf("batch after policy repair: %v", err)
	}
	old, err := store.AppByID(ctx, app.ID)
	if err != nil || old.Status != AppDeleted || old.DeletedAt == nil || old.DeleteGraceUntil == nil {
		t.Fatal("accepted batch did not retire app")
	}
	if mode == "preview" {
		set, err := store.(PRPreviewSetStore).GetPRPreviewSet(ctx, head.InstallationID, head.RepoFullName, head.PRNumber)
		if err != nil || set.CommitSHA != strings.Repeat("b", 40) || set.RootAppID == app.ID || len(set.MemberAppIDs) != 1 || old.Slug == app.Slug || old.PreviewPrState != PreviewPrStateStale {
			t.Fatal("accepted preview batch lost receipt or retirement")
		}
	} else {
		current, err := store.ProjectByID(ctx, project.ID)
		if err != nil || current.ScanSource != ProjectScanSourceCompose {
			t.Fatal("accepted project batch lost metadata")
		}
		crons, err := store.ListCronsForApp(ctx, app.ID)
		if err != nil || len(crons) != 0 {
			t.Fatal("accepted project batch left removed crons")
		}
	}
}

func TestMemTrafficAppBatchWithdrawal(t *testing.T) {
	for _, mode := range []string{"project", "preview"} {
		t.Run(mode, func(t *testing.T) {
			m, account, _, _, _ := memTrafficFixture(t)
			testTrafficAppBatchWithdrawal(t, m, account, mode, func(rule EdgeRule) { m.edgeRules[rule.ID] = rule }, func() string {
				return trafficTenantIntentDigest(t, []any{memTrafficAppIntent(t, m), m.projects, m.previewSets})
			})
		})
	}
}
