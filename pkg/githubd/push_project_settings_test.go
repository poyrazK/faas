// adr: 20261108090000
package githubd

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

// A push that changes the root gregale.yaml's routes says on a check that a
// reviewed deploy applies them; the services still deploy.
func TestPushReportsRouteChangesForAReviewedDeploy(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "web", RootDir: "web", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	fsys := fstest.MapFS{
		"web/index.ts":     &fstest.MapFile{Data: []byte("export const web = true;\n")},
		"web/package.json": &fstest.MapFile{Data: []byte("{}\n")},
		"gregale.yaml":     &fstest.MapFile{Data: []byte("routes:\n  - name: api\n    match_host: shop.example.com\n    match_path: /api/**\n    to: web\n")},
	}
	_, svc, rec, checks := pushRig(t, &scan, fsys)
	svc.ChangedFiles = &stubChangedFiles{files: []string{"gregale.yaml"}}
	body := []byte(`{"ref":"refs/heads/main","before":"base123","after":"head456","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	if _, err := svc.HandlePushRequest(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("builds = %d, want web deployed", len(rec.calls))
	}
	if !failedCheck(*checks, "demo (project settings)", "gregale deploy") {
		t.Fatalf("no project settings check: %+v", *checks)
	}
}

// A push that leaves gregale.yaml alone writes no settings check.
func TestPushWithoutManifestChangeWritesNoSettingsCheck(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "web", RootDir: "web", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	fsys := fstest.MapFS{
		"web/index.ts":     &fstest.MapFile{Data: []byte("export const web = true;\n")},
		"web/package.json": &fstest.MapFile{Data: []byte("{}\n")},
		"gregale.yaml":     &fstest.MapFile{Data: []byte("routes:\n  - name: api\n    match_host: shop.example.com\n    match_path: /api/**\n    to: web\n")},
	}
	_, svc, _, checks := pushRig(t, &scan, fsys)
	svc.ChangedFiles = &stubChangedFiles{files: []string{"web/index.ts"}}
	body := []byte(`{"ref":"refs/heads/main","before":"base123","after":"head456","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	if _, err := svc.HandlePushRequest(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if failedCheck(*checks, "demo (project settings)", "") {
		t.Fatalf("settings check without a gregale.yaml change: %+v", *checks)
	}
}
