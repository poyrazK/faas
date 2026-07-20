//go:build metal

// build_metal_test.go — M6 §14 acceptance: customer source tarball → apid →
// builderd → vmmd → firecracker → in-VM Railpack/buildctl → OCI image →
// imaged → snapshot_prime → deployments.Live. Closes issue #57.
//
// End-to-end through the real wire: apid accepts a multipart source
// upload (validateAndSpool, cmd/apid/deploy_inputs.go), emits a
// build_queued pg_notify, builderd claims the build row, asks vmmd to
// cold-boot a builder microVM using /srv/fc/base/builder-base.ext4 as
// drive0, the in-VM build dispatcher runs Railpack (Node/Python) or
// buildctl --frontend dockerfile (ADR-004), the produced OCI image is
// copied into the harness's build_export_dir and stamped onto the
// deployment row. imaged consumes the image, primes a snapshot, and marks
// the deployment Live (pkg/imaged/handler.go::handleAppChanged).
//
// Three subtests exercise the three framework paths that the §14 M6 gate
// requires (one each for node, python, dockerfile). The node subtest
// additionally runs the §14 oci-image-is-tarball assertion (#57 §4.5):
// it opens the produced OCI tarball at
// <TmpDir>/out/<build_id>/build/out/image.tar and asserts the standard
// OCI layout (oci-layout + index.json + blobs/sha256/<digest>). The
// dockerfile subtest additionally asserts the `buildctl` substring in
// build-done.json's LogTail so a regression that re-routed ADR-004 back
// through Railpack would surface immediately. The terminating state per
// subtest is state.DeployLive (WaitForDeploymentLive), not BuildSucceeded
// alone — a build that succeeded but failed imaged would still pass the
// weaker assertion, and §14 wants the full M6+M3 chain.
//
// Build tag: metal. Requires:
//   - /dev/kvm + root (jailer needs CAP_NET_ADMIN, CAP_MKNOD, …)
//   - Firecracker + the staged builder-base.ext4 on disk
//     (see deploy/lima/faas-metal.yaml for the rebuild script).
//   - FAAS_BUILDER_BASE_PATH pointing at the right builder-base.ext4
//     (the harness reads this env var via envBuilderBase).
//
// On the dev M3+ Mac: `make metal-lima`.
// On the EX44: `make test-metal`.

package e2e_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestBuildMetal exercises the M6 §14 orchestrator path for one app per
// framework. Each subtest is independent — fresh app + fresh account +
// fresh deployment — so a Python failure doesn't taint the Node path
// reporting. They share the harness (one booted daemons set) for speed.
//
// The shared harness is the e2etest.All set (apid + schedd + vmmd +
// imaged + gatewayd + builderd). gatewayd is needed because apid's deploy
// path also fans out an app_changed notification that gatewayd consumes —
// not strictly required for build_succeeded, but the harness already
// includes it and removing it would make this test asymmetric with
// deploy_wake_metal_test.go for no benefit.
//
// Issue #57 §4 calls out three orchestrator subtests AND a fourth
// `oci-image-is-tarball` assertion that opens the produced OCI tarball
// and verifies the OCI layout shape. The third (dockerfile) subtest also
// asserts ADR-004 dispatch via the `buildctl` substring in
// build-done.json's log_tail (guest-init's last 64 KiB of buildctl
// stdout).
func TestBuildMetal(t *testing.T) {
	// Pre-flight: skip cleanly when /dev/kvm isn't available so a dev box
	// (no KVM, no Lima) gets a precise message instead of a 90 s
	// builderd-vmmd handshake timeout.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal build test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal build test (no builder-base.ext4 path)")
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.Start(t, pool, e2etest.All)

	// One account, three apps (one per framework). Each subtest mints its
	// own app so deploys don't share state. We seed the account once at
	// the top because SeedAccount is idempotent and ~50 ms.
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	t.Run("node-tarball", func(t *testing.T) {
		buildID := runBuildSubtest(t, h, pool, key, "nodeapp", "node-app", NodeFixture(t), false)
		assertOCIImage(t, h, buildID)
	})
	t.Run("python-tarball", func(t *testing.T) {
		runBuildSubtest(t, h, pool, key, "pyapp", "python-app", PythonFixture(t), false)
	})
	t.Run("dockerfile-tarball", func(t *testing.T) {
		buildID := runBuildSubtest(t, h, pool, key, "dfapp", "dockerfile-app", DockerfileFixture(t), true)
		// ADR-004: the dockerfile path must dispatch to buildctl
		// --frontend dockerfile (NOT railpack). guest-init tail-captures
		// the last 64 KiB of build stdout into BuildDone.LogTail; the
		// buildctl CLI prints "[+] Building …" lines on its way to
		// loading the dockerfile frontend, so a substring match
		// distinguishes it from a Railpack-only run.
		assertBuildDoneSubstring(t, h, buildID, "buildctl")
	})
}

// runBuildSubtest drives a single end-to-end build, then asserts the
// deployment row reaches Live — the §14 M6 + M3 combined acceptance
// chain:
//
//  1. POST /v1/apps with the framework's slug → appID
//  2. POST /v1/apps/<slug>/deployments with multipart source tarball
//     + (optional) dockerfile flag → deploymentID + buildID
//  3. Wait for builds.status to reach BuildSucceeded (or BuildFailed).
//     This is the §14 M6 half: the in-VM Railpack/buildctl produced an
//     OCI image stampable on the deployment row.
//  4. Wait for deployments.status to reach DeployLive via
//     WaitForDeploymentLive. This is the §14 M3 half: imaged
//     consumed the OCI image, primed a snapshot, and marked the
//     deployment Live (pkg/imaged/handler.go::handleAppChanged).
//
// Without step 4, a build that succeeded but failed the imaged→snapshot
// leg would still report "passing" — that's why #57 calls this out as
// the proper M6+M3 terminal assertion.
//
// isDockerfile flips the multipart `dockerfile` field on so MapFramework
// (pkg/builderd/dispatch.go) routes to api.FrameworkDockerfile and the
// in-VM dispatcher invokes buildctl --frontend dockerfile (ADR-004).
//
// Returns the buildID so callers can post-inspect the produced OCI
// tarball at <TmpDir>/out/<build_id>/build/out/image.tar (the
// oci-image-is-tarball subtest) or read build-done.json (the
// buildctl-substring assertion).
func runBuildSubtest(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug, _ string, sourceTar []byte, isDockerfile bool) string {
	t.Helper()
	store := state.NewPgStore(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app"}); got != http.StatusCreated {
		t.Fatalf("create app %s: status=%d", slug, got)
	}
	appID := mustGetAppID(t, h, key, slug)

	depBody, depStatus := postMultipartDeployment(t, h, key, slug, sourceTar, isDockerfile)
	if depStatus != http.StatusAccepted {
		t.Fatalf("create deployment %s: status=%d body=%s", slug, depStatus, depBody)
	}
	depID, buildID := parseQueuedDeployment(t, depBody)

	// build_queued -> builderd picks up -> vm.Spawn -> in-VM build ->
	// OCI image produced -> UpdateBuildStatus(succeeded). The whole round
	// trip is ~60-180 s on Lima (buildctl cold cache; the dockerfile path
	// is faster because FROM busybox is in the builder VM). 6 min is the
	// outer deadline; the poll loop uses 5 min so a hung daemon still
	// leaves us room to report the last build state cleanly.
	build, err := e2etest.WaitForBuildStatus(ctx, t, pool, buildID,
		state.BuildSucceeded, 5*time.Minute)
	if err != nil {
		// Best-effort dump of the build row + log so a CI failure has
		// the in-VM buildctl/railpack stderr inline. The log file lives
		// at <FAAS_SPOOL_ROOT>/<deployment_id>/build.log on the harness
		// host (the apid subprocess's spool dir).
		if got, lerr := store.BuildByID(context.Background(), buildID); lerr == nil {
			t.Logf("build row at timeout: status=%s failure_class=%s log_path=%s",
				got.Status, got.FailureClass, got.LogPath)
			if got.LogPath != "" {
				if data, rerr := os.ReadFile(got.LogPath); rerr != nil {
					// Surface the read failure — silent failure looks
					// like "no log produced" when actually the read failed.
					t.Logf("read build.log at %q failed: %v", got.LogPath, rerr)
				} else {
					// Print at most the last 4 KiB so a multi-MB
					// build log doesn't blow up the test output.
					tail := data
					if len(tail) > 4096 {
						tail = tail[len(tail)-4096:]
					}
					t.Logf("build.log tail (last %d bytes):\n%s", len(tail), tail)
				}
			}
		}
		t.Fatalf("build %s did not reach succeeded: %v", buildID, err)
	}
	if build.FailureClass != "" {
		t.Errorf("build %s succeeded with non-empty failure_class=%q", buildID, build.FailureClass)
	}
	if build.StartedAt.IsZero() {
		t.Errorf("build %s: StartedAt is zero", buildID)
	}
	if build.FinishedAt.IsZero() {
		t.Errorf("build %s: FinishedAt is zero", buildID)
	}
	// Step 4 — the M3 chain. builderd.UpdateBuildStatus(succeeded)
	// emits a build_queued-done notify; imaged picks up the OCI image,
	// primes a snapshot, and MarkDeploymentLive fires. The deployment
	// row advances Building -> Live via deployment_changed.
	dep, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depID, 4*time.Minute)
	if err != nil {
		// Surface the last deployment status so a CI failure shows
		// whether we hung in 'building' or 'failed'.
		if last, lerr := store.DeploymentByID(context.Background(), depID); lerr == nil {
			t.Logf("deployment %s at timeout: status=%s error=%q", last.ID, last.Status, last.Error)
		}
		t.Fatalf("deployment %s did not reach live: %v", depID, err)
	}
	// Sanity: the app ID we got back from /v1/apps matches the deployment.
	if dep.AppID != appID {
		t.Errorf("deployment.AppID = %s, want %s", dep.AppID, appID)
	}
	return buildID
}

// assertOCIImage opens the OCI tarball at
// <Harness.TmpDir>/out/<build_id>/build/out/image.tar and asserts it
// contains the OCI Image Layout mandatory entries:
//
//   - oci-layout                    (image layout marker file)
//   - index.json                    (top-level image manifest list)
//   - blobs/sha256/<at least one>   (at least one content-addressable
//     layer or manifest blob)
//
// This is issue #57 §4 subtest 4 (`oci-image-is-tarball`). A passing
// assertion means the in-VM build dispatcher actually produced a
// well-formed OCI layout on disk — not just emitted an exit code 0.
// File presence is checked first so the failure message names the
// missing path instead of dumping a confusing tar error.
func assertOCIImage(t *testing.T, h *e2etest.Harness, buildID string) {
	t.Helper()
	imgPath := filepath.Join(h.TmpDir, "out", buildID, "build", "out", "image.tar")
	if _, err := os.Stat(imgPath); err != nil {
		t.Fatalf("OCI image.tar missing at %q: %v", imgPath, err)
	}
	f, err := os.Open(imgPath)
	if err != nil {
		t.Fatalf("open OCI image.tar: %v", err)
	}
	defer func() { _ = f.Close() }()
	// Cap the listing memory so a flaky build that produced a multi-GB
	// tarball doesn't OOM the test. The standard OCI layout is ~ a
	// dozen files for a hello-world image; 64 KiB is plenty.
	const tarListingCap = 64 * 1024
	tr := tar.NewReader(io.LimitReader(f, tarListingCap))
	got := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar Next: %v", err)
		}
		got[hdr.Name] = true
	}
	for _, want := range []string{"oci-layout", "index.json"} {
		if !got[want] {
			t.Errorf("OCI image at %s missing required entry %q (got %v)", imgPath, want, got)
		}
	}
	hasBlob := false
	for name := range got {
		if strings.HasPrefix(name, "blobs/sha256/") && len(name) > len("blobs/sha256/") {
			hasBlob = true
			break
		}
	}
	if !hasBlob {
		t.Errorf("OCI image at %s has no blobs/sha256/<digest> entry (got %v)", imgPath, got)
	}
}

// readBuildDone reads <Harness.TmpDir>/out/<build_id>/build-done.json
// (the same file vmmd's Destroy copies out of the builder chroot,
// pkg/vmmdgrpc/server.go). Used by assertBuildDoneSubstring to inspect
// guest-init's LogTail — a 64 KiB tail of the in-VM build stdout
// captured before the VM powers off.
func readBuildDone(t *testing.T, h *e2etest.Harness, buildID string) api.BuildDone {
	t.Helper()
	p := filepath.Join(h.TmpDir, "out", buildID, "build-done.json")
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read build-done.json at %q: %v", p, err)
	}
	var d api.BuildDone
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("decode build-done.json: %v body=%s", err, data)
	}
	return d
}

// assertBuildDoneSubstring asserts guest-init's captured LogTail
// contains substr. ADR-004 dispatch confirmation: the dockerfile path
// must invoke buildctl --frontend dockerfile, NOT railpack. Buildctl
// prints "[+] Building" / "buildctl: resolving" lines on the way to
// loading the dockerfile frontend, so a substring hit proves the binary
// ran. The inverse — Railpack-only — would print "Detected Node"
// / "Step 1/3" prefixes that do not contain "buildctl".
func assertBuildDoneSubstring(t *testing.T, h *e2etest.Harness, buildID, substr string) {
	t.Helper()
	d := readBuildDone(t, h, buildID)
	if d.ExitCode != 0 {
		t.Fatalf("build-done.exit_code = %d (want 0); log_tail=%.2f KB",
			d.ExitCode, float64(len(d.LogTail))/1024)
	}
	if !strings.Contains(d.LogTail, substr) {
		t.Fatalf("build-done.log_tail (%d bytes) does not contain %q\n---\n%s\n---",
			len(d.LogTail), substr, d.LogTail)
	}
}

// postMultipartDeployment builds a multipart/form-data body with the
// `source` file part (and optional `dockerfile` flag), POSTs it to apid's
// /v1/apps/<slug>/deployments, and returns the response body + status.
//
// Why this lives here instead of cmd/e2e/test_helpers.go: the multipart
// shape is specific to the build path (no image: field, source: required,
// optional dockerfile:). Keeping it next to the test that uses it makes
// the API contract obvious; if apid's createDeploymentMultipart drifts,
// the diff is in one place.
func postMultipartDeployment(t *testing.T, h *e2etest.Harness, key, slug string, sourceTar []byte, isDockerfile bool) ([]byte, int) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	srcPart, err := mw.CreateFormFile("source", "src.tar.gz")
	if err != nil {
		t.Fatalf("multipart CreateFormFile: %v", err)
	}
	if _, err := srcPart.Write(sourceTar); err != nil {
		t.Fatalf("multipart Write source: %v", err)
	}
	if isDockerfile {
		if err := mw.WriteField("dockerfile", "1"); err != nil {
			t.Fatalf("multipart WriteField dockerfile: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart Close: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/v1/apps/%s/deployments", h.APIDURL, slug), &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("deploy POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode
}

// parseQueuedDeployment extracts the deployment ID and build ID from a
// CreateDeploymentResponse. apid writes both into the JSON body when
// the deploy is queued (kind=tarball/dockerfile), but the response type
// for image: deploys omits the build ID — calling this on an image
// response panics, which is what we want (the test never sends image:
// in this file).
func parseQueuedDeployment(t *testing.T, body []byte) (deploymentID, buildID string) {
	t.Helper()
	// Minimal decode — CreateDeploymentResponse has more fields but we
	// only need two. Avoids importing the entire api surface here.
	var resp struct {
		ID     string `json:"id"`
		Build  string `json:"build"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode deployment response: %v body=%s", err, body)
	}
	if resp.ID == "" || resp.Build == "" {
		t.Fatalf("deployment response missing id/build: %s", body)
	}
	if !strings.EqualFold(resp.Status, "queued") {
		t.Logf("deployment %s status=%q (not 'queued' — apid may have started building already)", resp.ID, resp.Status)
	}
	return resp.ID, resp.Build
}
