// adr: 20261122220000
package githubd

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// A push to a branch that deploys a non-production scope never changes the
// app-wide contract that gates production.
func TestStagingPushDoesNotChangeTheVerificationContract(t *testing.T) {
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "web", RootDir: "web", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	rig := newRig(t, func(fs.FS) (reposcan.Result, error) { return scan, nil })
	project := rig.seedProject(t, "octo/api", "main")
	if err := rig.mem.ReplaceProjectDeployBranches(context.Background(), rig.acct, project.ID, map[string]string{"main": "production", "staging": "staging"}); err != nil {
		t.Fatal(err)
	}
	svc := newServiceForRig(t, rig)
	svc.Source = &stubSource{fsys: fstest.MapFS{
		"web/index.ts":            &fstest.MapFile{Data: []byte("export const web = true;\n")},
		"web/package.json":        &fstest.MapFile{Data: []byte("{}\n")},
		"web/gregale.verify.yaml": &fstest.MapFile{Data: []byte(pushContract)},
	}}
	rec := &recordingEnqueuer{buildID: "b-1"}
	svc.Enqueuer = rec
	body := []byte(`{"ref":"refs/heads/staging","after":"sha-staging","repository":{"full_name":"octo/api","name":"api"},"installation":{"id":42},"pusher":{"name":"alice"}}`)
	if _, err := svc.HandlePushRequest(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 1 || rec.calls[0].scope != "staging" {
		t.Fatalf("enqueue calls = %#v, want one staging build", rec.calls)
	}
	if _, err := rig.mem.AppVerificationContract(context.Background(), rec.calls[0].appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("staging push stored a contract: %v", err)
	}
}
