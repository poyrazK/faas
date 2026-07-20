//go:build metal

// build_metal_test.go — M6 §14 acceptance: customer source tarball → apid →
// builderd → vmmd → firecracker → in-VM Railpack/buildctl → build row
// transitions to succeeded. Closes issue #57.
//
// End-to-end through the real wire: apid accepts a multipart source
// upload (validateAndSpool, cmd/apid/deploy_inputs.go), emits a
// build_queued pg_notify, builderd claims the build row, asks vmmd to
// cold-boot a builder microVM using /srv/fc/base/builder-base.ext4 as
// drive0, the in-VM build dispatcher runs Railpack (Node/Python) or
// buildctl --frontend dockerfile (ADR-004), the produced OCI image is
// re-stamped onto the deployment row.
//
// Three subtests exercise the three framework paths that the §14 M6 gate
// requires (one each for node, python, dockerfile). Each runs through the
// full apid → builderd wire but stops at build_succeeded rather than going
// on to a wake — that's the M3 metal e2e's job (deploy_wake_metal_test.go).
// Issue #57 splits the gate into two phases deliberately: M6 owns
// "source → OCI image", M5 owns "OCI image → parked → wake".
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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
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
		runBuildSubtest(t, h, pool, key, "nodeapp", "node-app", NodeFixture(t), false)
	})
	t.Run("python-tarball", func(t *testing.T) {
		runBuildSubtest(t, h, pool, key, "pyapp", "python-app", PythonFixture(t), false)
	})
	t.Run("dockerfile-tarball", func(t *testing.T) {
		runBuildSubtest(t, h, pool, key, "dfapp", "dockerfile-app", DockerfileFixture(t), true)
	})
}

// runBuildSubtest drives a single end-to-end build:
//
//  1. POST /v1/apps with the framework's slug → appID
//  2. POST /v1/apps/<slug>/deployments with multipart source tarball
//     + (optional) dockerfile flag → deploymentID + buildID
//  3. Wait for builds.status to reach BuildSucceeded (or BuildFailed).
//     This is the §14 M6 acceptance assertion: the in-VM Railpack/buildctl
//     ran end-to-end and produced an OCI image.
//
// isDockerfile flips the multipart `dockerfile` field on so MapFramework
// (pkg/builderd/dispatch.go) routes to api.FrameworkDockerfile and the
// in-VM dispatcher invokes buildctl --frontend dockerfile (ADR-004).
func runBuildSubtest(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, key, slug, _ string, sourceTar []byte, isDockerfile bool) {
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
	// Cross-check: the deployment row must have moved past building too.
	// builderd.UpdateDeploymentStatus is the second pg_notify in the chain.
	dep, err := store.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatalf("read deployment %s: %v", depID, err)
	}
	if dep.Status == state.DeployBuilding {
		t.Errorf("deployment %s still in building after build succeeded", depID)
	}
	// Sanity: the app ID we got back from /v1/apps matches the deployment.
	if dep.AppID != appID {
		t.Errorf("deployment.AppID = %s, want %s", dep.AppID, appID)
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
