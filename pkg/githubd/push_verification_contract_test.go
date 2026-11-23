// adr: 20261122220000
package githubd

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

const pushContract = "version: 1\nchecks:\n  - {name: health, http: {path: /healthz}}\n"

func webPushRig(t *testing.T, files fstest.MapFS) (*testRig, *Service, *recordingEnqueuer, *[]heldCheck) {
	t.Helper()
	scan := reposcan.Result{Tier: reposcan.TierCompose, Workloads: []reposcan.Workload{
		{Class: reposcan.ClassHTTP, Name: "web", RootDir: "web", DetectedBy: reposcan.Detection{Detector: "compose"}},
	}}
	fsys := fstest.MapFS{
		"web/index.ts":     &fstest.MapFile{Data: []byte("export const web = true;\n")},
		"web/package.json": &fstest.MapFile{Data: []byte("{}\n")},
	}
	for name, file := range files {
		fsys[name] = file
	}
	return pushRig(t, &scan, fsys)
}

// A push stores the app's gregale.verify.yaml before the build is enqueued.
func TestPushAppliesTheAppsVerificationContract(t *testing.T) {
	rig, svc, rec, _ := webPushRig(t, fstest.MapFS{"web/" + "gregale.verify.yaml": &fstest.MapFile{Data: []byte(pushContract)}})
	push(t, svc, "head1")
	if len(rec.calls) != 1 {
		t.Fatalf("builds = %d, want 1", len(rec.calls))
	}
	stored, err := rig.mem.AppVerificationContract(t.Context(), rec.calls[0].appID)
	if err != nil || stored.CheckCount != 1 || stored.Revision != 1 {
		t.Fatalf("stored contract = %+v, %v", stored, err)
	}
	// The same file again is not a new revision.
	push(t, svc, "head2")
	if again, _ := rig.mem.AppVerificationContract(t.Context(), rec.calls[0].appID); again.Revision != 1 {
		t.Fatalf("unchanged contract rewritten: revision %d", again.Revision)
	}
}

// A root contract never lands on an app in a subdirectory, and a push
// without the file keeps the stored contract.
func TestPushIgnoresContractsOutsideTheAppRoot(t *testing.T) {
	rig, svc, rec, _ := webPushRig(t, fstest.MapFS{"gregale.verify.yaml": &fstest.MapFile{Data: []byte(pushContract)}})
	push(t, svc, "head1")
	if len(rec.calls) != 1 {
		t.Fatalf("builds = %d, want 1", len(rec.calls))
	}
	if _, err := rig.mem.AppVerificationContract(t.Context(), rec.calls[0].appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("root contract applied to web: %v", err)
	}
}

// An invalid contract stops the app's deploy and says why on its check.
func TestPushWithInvalidVerificationContractDoesNotDeploy(t *testing.T) {
	_, svc, rec, checks := webPushRig(t, fstest.MapFS{"web/gregale.verify.yaml": &fstest.MapFile{Data: []byte("version: 1\nchecks: [{name: x, http: {path: nope}}]\n")}})
	push(t, svc, "head1")
	if len(rec.calls) != 0 {
		t.Fatalf("builds = %d, want none", len(rec.calls))
	}
	found := false
	for _, c := range *checks {
		if c.summary != "" && failedCheck([]heldCheck{c}, c.slug, "invalid verification contract") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no failed check names the contract: %+v", *checks)
	}
}

func TestVerificationContractPath(t *testing.T) {
	for root, want := range map[string]string{"": "gregale.verify.yaml", ".": "gregale.verify.yaml", "web": "web/gregale.verify.yaml", "/services/api/": "services/api/gregale.verify.yaml"} {
		if got := verificationContractPath(root); got != want {
			t.Errorf("verificationContractPath(%q) = %q, want %q", root, got, want)
		}
	}
}
