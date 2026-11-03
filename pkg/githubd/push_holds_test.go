// adr: 20261103112845
package githubd

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

type heldCheck struct {
	slug    string
	phase   githubdgrpc.CheckPhase
	summary string
}

func pushRig(t *testing.T, scan *reposcan.Result, fsys fstest.MapFS) (*testRig, *Service, *recordingEnqueuer, *[]heldCheck) {
	t.Helper()
	rig := newRig(t, func(fs.FS) (reposcan.Result, error) { return *scan, nil })
	rig.seedProject(t, "octo/api", "main")
	svc := newServiceForRig(t, rig)
	if fsys != nil {
		svc.Source = &stubSource{fsys: fsys}
	}
	rec := &recordingEnqueuer{buildID: "b-1"}
	svc.Enqueuer = rec
	checks := &[]heldCheck{}
	svc.WriteAppCheck = func(_ context.Context, _ int64, _, _, slug string, phase githubdgrpc.CheckPhase, summary string) error {
		*checks = append(*checks, heldCheck{slug, phase, summary})
		return nil
	}
	return rig, svc, rec, checks
}

func push(t *testing.T, svc *Service, sha string) {
	t.Helper()
	body := []byte(`{"ref":"refs/heads/main","after":"` + sha + `","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	if _, err := svc.HandlePushRequest(context.Background(), body); err != nil {
		t.Fatalf("HandlePushRequest: %v", err)
	}
}

func failedCheck(checks []heldCheck, slug, text string) bool {
	for _, c := range checks {
		if c.slug == slug && c.phase == githubdgrpc.CheckPhaseFailed && strings.Contains(c.summary, text) {
			return true
		}
	}
	return false
}

// A push whose Compose file no longer lists a service keeps that service's
// app serving and says a reviewed deploy removes it.
func TestPushKeepsServiceMissingFromCompose(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "api", RootDir: "services/auth/api", DetectedBy: reposcan.Detection{Detector: "compose"}},
		{Class: reposcan.ClassHTTP, Name: "billing", RootDir: "services/billing", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	rig, svc, rec, checks := pushRig(t, &scan, nil)
	push(t, svc, "c0ffee01")
	apps, err := rig.mem.AppsForProject(context.Background(), rig.acct, mustProjectID(t, rig))
	if err != nil || len(apps) != 2 {
		t.Fatalf("first push apps = %d, %v", len(apps), err)
	}
	scan.Workloads = scan.Workloads[:1] // billing left the Compose file
	rec.calls = nil
	push(t, svc, "c0ffee02")
	apps, err = rig.mem.AppsForProject(context.Background(), rig.acct, mustProjectID(t, rig))
	if err != nil {
		t.Fatal(err)
	}
	var billing state.App
	for _, app := range apps {
		if app.WorkloadName == "billing" {
			billing = app
		}
	}
	if billing.ID == "" || billing.Status == state.AppDeleted {
		t.Fatalf("billing removed by a push: %+v", apps)
	}
	for _, call := range rec.calls {
		if call.appID == billing.ID {
			t.Fatal("kept billing was redeployed")
		}
	}
	if !failedCheck(*checks, billing.Slug, "does not remove services") {
		t.Fatalf("no kept check for billing: %+v", *checks)
	}
}

// A service that needs a Compose postgres a push cannot create is not
// deployed by the push; once its variable is set, the next push deploys it.
func TestPushHoldsServiceWaitingForComposePostgres(t *testing.T) {
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  api:
    build: ./services/auth/api
    environment:
      DATABASE_URL: postgres://app:secret@db:5432/app
  db:
    image: postgres:16
`)},
		"services/auth/api/index.ts":     &fstest.MapFile{Data: []byte("export const auth = true;\n")},
		"services/auth/api/package.json": &fstest.MapFile{Data: []byte("{}\n")},
	}
	scan := reposcan.Result{Tier: 1,
		Workloads: []reposcan.Workload{{Class: reposcan.ClassHTTP, Name: "api", RootDir: "services/auth/api", EnvKeys: []string{"DATABASE_URL"}, DetectedBy: reposcan.Detection{Detector: "compose"}}},
		Managed:   []reposcan.Managed{{Name: "db", Kind: "postgres", EnvHint: "DATABASE_URL"}},
	}
	rig, svc, rec, checks := pushRig(t, &scan, fsys)
	push(t, svc, "c0ffee01")
	apps, err := rig.mem.AppsForProject(context.Background(), rig.acct, mustProjectID(t, rig))
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps = %d, %v", len(apps), err)
	}
	api := apps[0]
	if len(rec.calls) != 0 {
		t.Fatalf("api deployed without DATABASE_URL: %+v", rec.calls)
	}
	if !failedCheck(*checks, api.Slug, "DATABASE_URL") {
		t.Fatalf("no database check for api: %+v", *checks)
	}
	if err := rig.mem.UpsertAppEnvInScope(context.Background(), rig.acct, api.ID, state.DefaultEnvScope, "DATABASE_URL", "postgres://elsewhere/app"); err != nil {
		t.Fatal(err)
	}
	push(t, svc, "c0ffee02")
	if len(rec.calls) != 1 || rec.calls[0].appID != api.ID {
		t.Fatalf("api not deployed once its variable is set: %+v", rec.calls)
	}
}

func mustProjectID(t *testing.T, rig *testRig) string {
	t.Helper()
	project, err := rig.mem.ProjectBySlug(context.Background(), rig.acct, "demo")
	if err != nil {
		t.Fatal(err)
	}
	return project.ID
}

// A deleted app keeps its slug through the restore window (apps_slug_key).
// A push that brought its service back failed the whole reconcile on the slug
// conflict; it now holds that service, says how to restore it, and deploys
// the rest.
func TestPushHoldsServiceWhoseSlugADeletedAppHolds(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "api", RootDir: "services/auth/api", DetectedBy: reposcan.Detection{Detector: "compose"}},
		{Class: reposcan.ClassHTTP, Name: "billing", RootDir: "services/billing", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	rig, svc, rec, checks := pushRig(t, &scan, nil)
	push(t, svc, "c0ffee01")
	ctx := context.Background()
	apps, err := rig.mem.AppsForProject(ctx, rig.acct, mustProjectID(t, rig))
	if err != nil || len(apps) != 2 {
		t.Fatalf("first push apps = %d, %v", len(apps), err)
	}
	var billing, api state.App
	for _, app := range apps {
		switch app.WorkloadName {
		case "billing":
			billing = app
		case "api":
			api = app
		}
	}
	if _, err := rig.mem.SoftDeleteAppCascade(ctx, billing.ID); err != nil {
		t.Fatal(err)
	}
	rec.calls = nil
	push(t, svc, "c0ffee02")
	if !failedCheck(*checks, billing.Slug, "gregale apps restore "+billing.Slug) {
		t.Fatalf("no restore check for %s: %+v", billing.Slug, *checks)
	}
	deployed := false
	for _, call := range rec.calls {
		deployed = deployed || call.appID == api.ID
	}
	if !deployed {
		t.Fatalf("api not deployed beside the held service: %+v checks %+v", rec.calls, *checks)
	}
	apps, err = rig.mem.AppsForProject(ctx, rig.acct, mustProjectID(t, rig))
	if err != nil || len(apps) != 1 || apps[0].ID != api.ID {
		t.Fatalf("apps after the held push = %+v, %v", apps, err)
	}
}
