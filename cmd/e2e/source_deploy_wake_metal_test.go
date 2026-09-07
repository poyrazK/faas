//go:build metal

// source_deploy_wake_metal_test.go — issue #735 / DEPLOY-PROV-1 acceptance:
// the full chain a customer hits on first deploy.
//
//	CLI multipart upload
//	  → apid createDeploymentMultipart (validateAndSpool + extract)
//	    → pg_notify build_queued
//	      → builderd claim slot → cold-boot builder microVM (ADR-003)
//	        → in-VM Railpack Node build → OCI image stamp on deployment row
//	          → imaged prime snapshot → deployment = Live (BuildSucceeded path)
//	            → schedd admit → vmmd cold boot → guest-init ready
//	              → RUNNING → park → snapshot (ADR-005 cold-boot-always-works)
//
// No single existing metal test ties source-tarball deploy → wake → cold-boot
// fallback together. deploy_wake_metal_test.go covers image deploy → wake
// (and the 100-cycle latency loop); build_metal_test.go covers source
// tarball → builderd → imaged → Live but stops before any wake. The chain
// between them is the gap this test closes.
//
// Eight subtests run sequentially against one harness (apid + schedd +
// imaged + vmmd + gatewayd + builderd + meterd) and one PG schema:
//
//  1. source-deployed-live       — Node fixture → multipart → DeployLive
//  2. first-park                 — explicit park → StateParked, zero resident
//  3. wake-from-snapshot         — HTTP probe → x-faas-wake-id → method=restore
//  4. idle-repark                — reaper parks the running instance
//  5. force-stale-snapshot       — direct SQL update snapshots.fc_version
//  6. wake-from-cold-boot        — HTTP probe → x-faas-wake-id → method=cold_boot
//  7. vmmd-restore-fail-fallback — corrupt snapshot file → restore error → cold_boot
//  8. idempotent-replay          — same Idempotency-Key → same deploy ID
//
// Build tag: metal. Requires /dev/kvm + root + Firecracker on PATH +
// FAAS_TEST_KERNEL + FAAS_BUILDER_BASE_PATH. Runs on EX44 via `make
// test-metal` and on M3+ Mac via `make metal-lima`. Wall-clock target
// ≤ ~6 min; fits inside the existing 60-min -timeout budget used by
// deploy_wake_metal_test.go.
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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// sourceDeployHelloBody is the marker the fixture's index.js returns; the
// wake subtests assert the body round-trips through gatewayd. The exact
// string matches what NodeFixture writes (cmd/e2e/fixtures_test.go:53).
const sourceDeployHelloBody = "hello from faas (node fixture)"

// TestSourceDeployWakeMetal runs the seven-subtest chain. The harness
// shape mirrors deploy_wake_metal_test.go: same fake registry, same
// builder-base / deploy-base layers, same Hobby plan (RequireAuthn
// flipped off so anonymous probes work).
func TestSourceDeployWakeMetal(t *testing.T) {
	// Pre-flight: KVM + builder-base required — same gate as the existing
	// two metal tests, so a CI box without /dev/kvm gets one clean
	// message instead of a 90s builderd→vmmd handshake timeout.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal source-deploy→wake test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping metal source-deploy→wake test")
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Fake registry + builder/deploy base layers. Mirrors
	// deploy_wake_metal_test.go:84-98 so the fixture's hello.txt lands in
	// `above` after oci.LayersAboveBase subtracts the deploy base.
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", sourceDeployHelloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")
	// Opt the reference-node acceptance into the public gateway smoke. The
	// harness passes the actual gateway origin to imaged, so this verifies the
	// same route a developer's first API request will use.
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")

	// Full metal set: deploy_wake_metal_test.go uses DeployWake
	// (no builderd) but the source-deploy path needs builderd to claim
	// the build_queued pg_notify and spin up a builder microVM. Use All.
	h := e2etest.Start(t, pool, e2etest.All)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Hobby defaults to require_authn=true post #695 / ADR-080. The wake
	// subtests probe through the gateway as anonymous requests; opt out
	// at create time.
	falsy := false
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "srcdeploy", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "srcdeploy")

	// Idle timeout dialed down to spec §4.3 floor so the reaper settles
	// each subtest's idle-repark within one tick — same trick
	// deploy_wake_metal_test.go:233 uses for its 100-cycle loop.
	setAppIdleTimeout(t, h, key, "srcdeploy", api.IdleTimeoutFloorSeconds)

	// Build the fixture once; both the live assertion (subtest 1) and
	// the idempotency replay (subtest 8) upload the same bytes.
	sourceTar := NodeFixture(t)

	// Drive the multipart deploy. Capture deploymentID + buildID so the
	// later subtests can wait on state.DeployLive / state.BuildSucceeded
	// without re-fetching the deployment row.
	depBody, depStatus := postMultipartDeployment(t, h, key, "srcdeploy", sourceTar, false, "")
	if depStatus != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", depStatus, depBody)
	}
	depID, buildID := parseQueuedDeployment(t, depBody)

	// -- 1. source-deployed-live -----------------------------------------
	// The full M3+M6 chain: builderd → in-VM Railpack → imaged → Live.
	// WaitForBuildStatus covers the M6 half (build row reaches
	// BuildSucceeded); WaitForDeploymentLive covers the M3 half (imaged
	// primed the snapshot and stamped the deployment Live). A green
	// both means the customer source → running app wire path is whole.
	t.Run("source-deployed-live", func(t *testing.T) {
		defer h.DumpLogs(t)

		bctx, bcancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer bcancel()
		if _, err := e2etest.WaitForBuildStatus(bctx, t, pool, buildID, state.BuildSucceeded, 5*time.Minute); err != nil {
			t.Fatalf("build %s did not reach succeeded: %v", buildID, err)
		}
		dctx, dcancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer dcancel()
		dep, err := e2etest.WaitForDeploymentLive(dctx, t, pool, depID, 4*time.Minute)
		if err != nil {
			t.Fatalf("deployment %s did not reach live: %v", depID, err)
		}
		if dep.AppID != appID {
			t.Errorf("deployment.AppID = %s, want %s", dep.AppID, appID)
		}
		receipt, err := apihostingreceipt.Decode(dep.APIHostingReceipt)
		if err != nil {
			t.Fatalf("hosting receipt: %v", err)
		}
		if receipt.DeploymentID != dep.ID || receipt.AppID != appID {
			t.Fatalf("hosting receipt identity = deployment %s/app %s, want %s/%s", receipt.DeploymentID, receipt.AppID, dep.ID, appID)
		}
		if receipt.Profile.Framework != "node" || receipt.Profile.HealthPath == "" {
			t.Fatalf("hosting profile = %+v, want inferred node profile", receipt.Profile)
		}
		if receipt.Smoke.Status != apihostingreceipt.SmokeVerified || receipt.Smoke.StatusCode < http.StatusOK || receipt.Smoke.StatusCode >= http.StatusMultipleChoices {
			t.Fatalf("hosting smoke = %+v, want verified 2xx", receipt.Smoke)
		}
		t.Logf("source-deployed-live: dep=%s build=%s live", dep.ID, buildID)
	})

	// -- 2. first-park ----------------------------------------------------
	// Explicit park (POST /v1/apps/{slug}/park → apid sets intent →
	// schedd drives vmmd into a snapshot). Wait for StateParked and
	// confirm the live instance count went to zero (the §6.2 invariant:
	// "a parked app consumes zero resident RAM").
	t.Run("first-park", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/srcdeploy/park", nil); status != http.StatusAccepted {
			t.Fatalf("park request: status=%d", status)
		}
		ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 25*time.Second)
		if err != nil {
			t.Fatalf("no parked instance: %v", err)
		}
		if len(ins) == 0 {
			t.Fatal("no instances after park")
		}
		t.Logf("first-park: instance=%s parked", ins[0].ID)
	})

	// -- 3. wake-from-snapshot -------------------------------------------
	// First HTTP probe through gatewayd. The gateway holds the request
	// during wake (CLAUDE.md gotcha: queue cap 512/30 s) and stamps the
	// x-faas-wake-id response header from the wake it just admitted.
	// Assert method=restore in the boot_completed event tied to that
	// wake_id — the snapshot is still usable (fc_version matches,
	// stale=false), so the planner picks restore and vmmd honors it.
	var firstWakeID string
	t.Run("wake-from-snapshot", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		url := gatewayAppURL(h, "srcdeploy")
		client := h.HTTPClient()
		if err := e2etest.WaitForHTTPReady(ctx, t, client, url, 5*time.Second); err != nil {
			t.Fatalf("gateway not ready: %v", err)
		}
		body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, "srcdeploy.apps.test.example", 30*time.Second)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if got := strings.TrimSpace(string(body)); got != sourceDeployHelloBody {
			t.Fatalf("body=%q want %q", got, sourceDeployHelloBody)
		}
		if wakeID == "" {
			t.Fatal("gateway response missing x-faas-wake-id header")
		}
		firstWakeID = wakeID

		// The wake already completed by the time the response returns
		// (gateway blocks until schedd records the runtime), so the
		// boot_completed event should land within a couple of polls.
		wctx, wcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer wcancel()
		ev, err := e2etest.WaitForWakeMethod(wctx, t, pool, wakeID, "restore", 10*time.Second)
		if err != nil {
			t.Fatalf("wake %s did not complete via restore: %v", wakeID, err)
		}
		t.Logf("wake-from-snapshot: wake_id=%s method=restore instance=%s", wakeID, instanceIDFromEvent(ev))
	})

	// -- 4. idle-repark --------------------------------------------------
	// Wait for the schedd reaper to park the just-woken instance. The
	// reaper tick is hardcoded at 10s in pkg/sched/loop.go:103 and our
	// idle_timeout is dialed to the §4.3 floor (10s), so worst case is
	// ~20s. Assert the same instance ID is reused (no churn).
	t.Run("idle-repark", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 25*time.Second)
		if err != nil {
			t.Fatalf("instance did not re-park: %v", err)
		}
		if len(ins) == 0 {
			t.Fatal("no instances after re-park")
		}
		t.Logf("idle-repark: instance=%s re-parked", ins[0].ID)
	})

	// -- 5. force-stale-snapshot -----------------------------------------
	// No public HTTP API exists for changing a snapshot's fc_version
	// (verified via the codebase map in issue #735). The cleanest e2e
	// seam is a direct SQL UPDATE — the partial unique index from
	// migrations/00110_snapshots_tier.sql only constrains stale=false
	// rows, so flipping fc_version on the active snapshot survives.
	t.Run("force-stale-snapshot", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tag, err := pool.Exec(ctx, `
			update snapshots
			   set fc_version = $2
			 where deployment_id = $1
			   and stale = false
		`, depID, "test-incompatible-fc-version")
		if err != nil {
			t.Fatalf("force-stale SQL update: %v", err)
		}
		if tag.RowsAffected() == 0 {
			t.Fatal("no non-stale snapshot rows updated — was subtest 1's snapshot already marked stale?")
		}
		t.Logf("force-stale-snapshot: %d snapshot rows updated to incompatible fc_version", tag.RowsAffected())
	})

	// -- 6. wake-from-cold-boot ------------------------------------------
	// HTTP probe through gatewayd. Scheduler's usableSnapshotForWake
	// (pkg/sched/engine.go:3516-3565) sees the fc_version mismatch and
	// returns cold_boot_fallback — vmmd cold-boots from the deploy
	// base, not from the snapshot. schedd's wake path then calls
	// MarkSnapshotStale on the bad snapshot, which is the exact
	// restore-fell-back-to-cold-boot path documented at
	// pkg/sched/engine.go:1568-1578. Assert method=cold_boot in the
	// boot_completed event (not boot_started — that's the PLANNED
	// method, which would also report cold_boot here, but the test
	// should exercise the authoritative completion signal).
	t.Run("wake-from-cold-boot", func(t *testing.T) {
		defer h.DumpLogs(t)
		url := gatewayAppURL(h, "srcdeploy")
		client := h.HTTPClient()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, "srcdeploy.apps.test.example", 60*time.Second)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if got := strings.TrimSpace(string(body)); got != sourceDeployHelloBody {
			t.Fatalf("body=%q want %q", got, sourceDeployHelloBody)
		}
		if wakeID == "" {
			t.Fatal("gateway response missing x-faas-wake-id header")
		}
		if wakeID == firstWakeID {
			t.Errorf("cold-boot wake reused wake_id %s; expected a fresh wake", wakeID)
		}

		// boot_completed is emitted by schedd post-RecordRuntime; the
		// HTTP response has already returned so the event is in the DB.
		wctx, wcancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer wcancel()
		ev, err := e2etest.WaitForWakeMethod(wctx, t, pool, wakeID, "cold_boot", 10*time.Second)
		if err != nil {
			t.Fatalf("wake %s did not complete via cold_boot: %v", wakeID, err)
		}
		t.Logf("wake-from-cold-boot: wake_id=%s method=cold_boot instance=%s", wakeID, instanceIDFromEvent(ev))

		// Independent confirmation that schedd invoked MarkSnapshotStale
		// on the bad snapshot (pkg/sched/engine.go:1568-1578). The
		// engine sets stale=true on the original snapshot ID; we read
		// it back to assert the side effect landed.
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		var staleCount int
		if err := pool.QueryRow(sctx,
			`select count(*) from snapshots where deployment_id = $1 and stale = true`, depID,
		).Scan(&staleCount); err != nil {
			t.Fatalf("query snapshots.stale: %v", err)
		}
		if staleCount == 0 {
			t.Error("no snapshot rows marked stale after cold-boot fallback; MarkSnapshotStale didn't fire")
		}
	})
	// Note: subtest 6 leaves the instance RUNNING. Subtest 7 needs it
	// parked again before it can re-wake, so we explicitly park + wait
	// for StateParked inside the new subtest rather than relying on the
	// reaper (which would add a 10-20s tick we'd otherwise have to budget).

	// -- 7. vmmd-restore-fail-fallback -----------------------------------
	// Subtest 6 exercised the PLANNER-side cold-boot fallback: the
	// scheduler saw a fc_version mismatch in the snapshots row and never
	// even asked vmmd to restore. This subtest exercises the VMM-SIDE
	// fallback: the planner picks the snapshot (fc_version matches,
	// stale=false) and vmmd's m.vmm.Restore(...) call fails because the
	// on-disk snapshot file is corrupt. vmmd then logs "restore failed,
	// falling back to cold boot" (pkg/fcvm/manager.go:2368) and returns
	// WakeColdBoot; schedd's wake path (pkg/sched/engine.go:1568-1578)
	// observes haveSnap=true && Method==WAKE_COLD_BOOT and calls
	// MarkSnapshotStale on the bad snapshot. Two distinct fallback paths
	// must both succeed — only one is the "intended" ADR-005 path.
	//
	// Path layout: snapshots.storage_key carries "snap/<depID>/mem" and
	// the LocalStorageBackend joins it under <root>/<key>, so the
	// harness's h.ImagedTmp + "snap/" + depID + "/mem" is the file we
	// corrupt. After subtest 6's cold-boot wake, schedd re-snapshotted
	// the freshly-booted VM (engine.go:3154 emits the new snapshot_written
	// payload), so the on-disk file is fresh and non-empty — perfect
	// target for a truncate.
	t.Run("vmmd-restore-fail-fallback", func(t *testing.T) {
		defer h.DumpLogs(t)

		// Park the running instance from subtest 6 so the next wake is
		// a real wake (not a hot forward). The reaper tick is 10s; an
		// explicit park + 25s wait mirrors subtest 2.
		pctx, pcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer pcancel()
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/srcdeploy/park", nil); status != http.StatusAccepted {
			t.Fatalf("park: status=%d", status)
		}
		if _, err := e2etest.WaitForInstanceState(pctx, t, pool, appID, state.StateParked, 25*time.Second); err != nil {
			t.Fatalf("instance did not park after subtest 6 wake: %v", err)
		}

		// The cold-boot re-prime (engine.go:3154) wrote a fresh
		// non-stale snapshots row; planner will pick it on the next
		// wake unless the file is corrupt. Sanity-check the planner
		// state BEFORE we corrupt the file, so the test's diagnosis is
		// clear if a future seam regresses (e.g. sched stops re-priming
		// after cold-boot, leaving the planner with no row to pick).
		sctx, scancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer scancel()
		var preCount int
		if err := pool.QueryRow(sctx,
			`select count(*) from snapshots where deployment_id = $1 and stale = false`, depID,
		).Scan(&preCount); err != nil {
			t.Fatalf("count non-stale snapshots: %v", err)
		}
		if preCount == 0 {
			t.Fatal("no non-stale snapshots before corrupt; subtest 6's cold-boot re-prime didn't fire — test cannot proceed")
		}

		// Corrupt the on-disk mem file. The key the planner will hand
		// to vmmd is "snap/<depID>/mem" — see pkg/state/keys.go:55
		// (SnapMemKey). LocalStorageBackend.join (pkg/storage/local.go:85)
		// resolves keys under root, so the absolute path is
		// <h.ImagedTmp>/snap/<depID>/mem.
		//
		// We write a partial block of garbage (not 0 bytes — LocalStorageBackend.Get
		// at pkg/storage/local.go:209 treats a 0-byte file as not-found
		// and we'd get a different error path: "no usable snapshot" at
		// the planner layer, NOT the vmmd restore-fail branch we're
		// pinning). The Firecracker snapshot loader parses the header
		// and bails out with a non-recognisable magic number.
		snapPath := filepath.Join(h.ImagedTmp, "snap", depID, "mem")
		preStat, err := os.Stat(snapPath)
		if err != nil {
			t.Fatalf("stat snapshot file before corrupt: %v (path=%s)", err, snapPath)
		}
		if preStat.Size() == 0 {
			t.Fatalf("snapshot file is empty (%s); subtest 6's cold-boot re-prime didn't write a snapshot — test cannot proceed", snapPath)
		}
		// Overwrite the first 4 KiB with non-zero garbage so Firecracker's
		// snapshot loader fails its magic-number / version check on the
		// very first read. We don't truncate the file — Firecracker reads
		// metadata at the tail (snapshot layout depends on the page count,
		// which the loader computes from file size); a 4 KiB garbage
		// header is enough to trip the magic check, and keeping the rest
		// of the bytes intact means the failure is unambiguously a header
		// parse error rather than a truncated-file stat error.
		if err := writeGarbage(snapPath, 4096); err != nil {
			t.Fatalf("write garbage to snapshot file %s: %v", snapPath, err)
		}
		t.Logf("vmmd-restore-fail-fallback: corrupted snapshot file at %s (was %d bytes)", snapPath, preStat.Size())

		// Wake through gatewayd. Planner sees the row (fc_version
		// matches, stale=false) → picks WakeRestore → vmmd attempts
		// Restore → file parse fails → cold-boot fallback → scheduler
		// marks the bad snapshot stale. Method on boot_completed is
		// cold_boot; MarkSnapshotStale flips stale=true on the snapshot
		// row vmmd was given.
		wctx, wcancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer wcancel()
		url := gatewayAppURL(h, "srcdeploy")
		client := h.HTTPClient()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, client, url, "srcdeploy.apps.test.example", 60*time.Second)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if got := strings.TrimSpace(string(body)); got != sourceDeployHelloBody {
			t.Fatalf("body=%q want %q", got, sourceDeployHelloBody)
		}
		if wakeID == "" || wakeID == firstWakeID {
			t.Fatalf("wake_id=%q (first=%q); expected a fresh wake_id", wakeID, firstWakeID)
		}

		// Assert the AUTHORITATIVE completion signal: schedd re-emits
		// boot_completed with Method=cold_boot after vmmd returns from
		// the failed Restore + ColdBoot leg.
		ev, err := e2etest.WaitForWakeMethod(wctx, t, pool, wakeID, "cold_boot", 30*time.Second)
		if err != nil {
			t.Fatalf("wake %s did not complete via cold_boot after restore failure: %v", wakeID, err)
		}
		t.Logf("vmmd-restore-fail-fallback: wake_id=%s method=cold_boot instance=%s", wakeID, instanceIDFromEvent(ev))

		// Independent log-grep confirmation: vmmd emits the exact line
		// "restore failed, falling back to cold boot" from
		// pkg/fcvm/manager.go:2368. If a future refactor moves the
		// fallback inside the VMM layer (where we can't observe it
		// from outside), the planner-side MarkSnapshotStale still fires
		// but this log assertion catches the change.
		logs := h.VmmdLogs()
		if !strings.Contains(logs, "restore failed, falling back to cold boot") {
			t.Errorf("vmmd log missing 'restore failed, falling back to cold boot' line; the warn at pkg/fcvm/manager.go:2368 didn't fire — fallback path regressed")
		}

		// Independent DB confirmation: MarkSnapshotStale flipped the
		// bad snapshot's stale flag. After this subtest there should be
		// at least one stale row for the deployment (the bad one) AND
		// at least one non-stale row (the re-prime after the
		// cold-boot that vmmd just did — engine.go:3154 fires after
		// every successful cold-boot wake).
		mctx, mcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer mcancel()
		var staleAfter, freshAfter int
		if err := pool.QueryRow(mctx,
			`select count(*) from snapshots where deployment_id = $1 and stale = true`, depID,
		).Scan(&staleAfter); err != nil {
			t.Fatalf("count stale snapshots: %v", err)
		}
		if err := pool.QueryRow(mctx,
			`select count(*) from snapshots where deployment_id = $1 and stale = false`, depID,
		).Scan(&freshAfter); err != nil {
			t.Fatalf("count non-stale snapshots: %v", err)
		}
		if staleAfter == 0 {
			t.Errorf("no stale snapshot rows after restore-fail wake; schedd's MarkSnapshotStale branch at engine.go:1568-1578 didn't fire")
		}
		if freshAfter == 0 {
			t.Errorf("no fresh non-stale snapshot rows after cold-boot wake; engine.go:3154 re-prime didn't fire")
		}
		t.Logf("vmmd-restore-fail-fallback: snapshots stale=%d fresh=%d", staleAfter, freshAfter)
	})

	// -- 8. idempotent-replay --------------------------------------------
	// Re-issue the SAME multipart POST with the SAME Idempotency-Key.
	// apid's idempotent middleware (cmd/apid/server.go:1647-1667)
	// replays the cached 202 response body and stamps the
	// Idempotent-Replayed: true response header. Verify: (a) same
	// deployment ID returned, (b) Idempotent-Replayed header is "true",
	// (c) no new deployment row was created in the DB. This pins the
	// guarantee that re-running `gregale deploy` with the same source
	// (e.g., a CI retry) doesn't trigger a duplicate build.
	t.Run("idempotent-replay", func(t *testing.T) {
		// First, count deployment rows so we can detect any drift.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var beforeCount int
		if err := pool.QueryRow(ctx,
			`select count(*) from deployments where app_id = $1`, appID,
		).Scan(&beforeCount); err != nil {
			t.Fatalf("count deployments before: %v", err)
		}

		// Two POSTs with the SAME Idempotency-Key. The first seeds
		// the cache (handler runs once), the second must replay
		// (handler does NOT run). postMultipartDeployment doesn't
		// return response headers, so we hand-build the requests to
		// capture Idempotent-Replayed. The SDK's
		// Client.DeployMultipart (pkg/api/client.go:460-501)
		// auto-mints a fresh UUIDv4 per call — wrong shape for a
		// replay test, so we use multipart directly.
		idemKey := fixedIdempotencyKey()
		body1, status1, replayed1 := postMultipartCapturingHeaders(t, h, key, "srcdeploy", sourceTar, false, idemKey)
		if status1 != http.StatusAccepted {
			t.Fatalf("first POST: status=%d body=%s", status1, body1)
		}
		if replayed1 {
			t.Errorf("first POST marked as replay; idempotency cache should be cold")
		}
		body2, status2, replayed2 := postMultipartCapturingHeaders(t, h, key, "srcdeploy", sourceTar, false, idemKey)
		if status2 != http.StatusAccepted {
			t.Fatalf("replay POST: status=%d body=%s", status2, body2)
		}
		if !replayed2 {
			t.Errorf("second POST did not set Idempotent-Replayed: true; apid middleware didn't replay")
		}

		// Both responses must match the original depID.
		for i, body := range [][]byte{body1, body2} {
			var resp struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				t.Fatalf("decode POST #%d response: %v body=%s", i+1, err, body)
			}
			if resp.ID != depID {
				t.Errorf("POST #%d returned deployment %s; want original %s", i+1, resp.ID, depID)
			}
		}

		// Independent DB check: no new deployment row was created.
		var afterCount int
		if err := pool.QueryRow(ctx,
			`select count(*) from deployments where app_id = $1`, appID,
		).Scan(&afterCount); err != nil {
			t.Fatalf("count deployments after: %v", err)
		}
		if afterCount != beforeCount {
			t.Errorf("deployment row count grew from %d to %d on replay; expected unchanged", beforeCount, afterCount)
		}
		t.Logf("idempotent-replay: same dep=%s; deployments=%d before, %d after", depID, beforeCount, afterCount)
	})

	// -- 9. auto-pick-function-from-handler-js -------------------------
	// Issue #737 / ADR-083 acceptance: a function-shaped tarball
	// (`handler.js` only at the root) reaches apid with the new
	// wire shape (`runtime=node22` + `handler=handler.handler`
	// multipart form fields + `Type=function` on CreateApp) and
	// apid's function-deploy path returns 202 with a fresh
	// deployment ID. Pins the CLI's new `gregale deploy` zero-config
	// function path end-to-end at the wire boundary.
	//
	// We deliberately stop at the 202 — a full live + wake + invoke
	// cycle would double the wall-clock of this test (~6 min target
	// per the file header) and isn't needed here: the function wire
	// contract is what this PR adds, and the function-deploy path
	// beyond the wire is pinned by cmd/apid/deploy_inputs_test.go
	// (TestDeployFunction_Multipart, lines 516-593) and
	// cmd/apid/handlers_test.go (TestCreateApp_FunctionRuntime*).
	t.Run("auto-pick-function-from-handler-js", func(t *testing.T) {
		// Fresh function-shaped app (Type=function on CreateApp
		// matches what the CLI's auto-detect path sets when the
		// cwd is `handler.js`-only).
		if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{
			Slug:         "srcfunc",
			Type:         "function",
			Runtime:      "node22",
			RequireAuthn: &falsy,
		}); got != http.StatusCreated {
			t.Fatalf("create function app: status=%d", got)
		}
		funcAppID := mustGetAppID(t, h, key, "srcfunc")
		// apps.runtime is already set by the CreateApp call above
		// (cmd/apid/handlers.go:202 stamps req.Runtime onto the row).
		// No direct SQL update needed — the multipart validator at
		// cmd/apid/deploy_inputs.go:144 reads apps.runtime to
		// accept the function deploy.

		// Tarball content: a single handler.js. Pack with the
		// same NodeFixture helper but rename the inner file to
		// handler.js so the function-layer manifest can find it.
		// The CLI's auto-detect path emits exactly this wire
		// shape (`runtime=node22` + `handler=handler.handler`)
		// when a customer with a `handler.js`-only cwd runs
		// `gregale deploy`.
		funcTar := FunctionNodeFixture(t)

		// Hand-built multipart — mirrors postMultipartDeployment
		// but with the new function form fields. Duplicated
		// because postMultipartDeployment doesn't accept
		// runtime/handler (those are CLI-side fields, not
		// ad-hoc test surface) and adding optional form fields
		// to that helper would muddy its existing callers.
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		srcPart, err := mw.CreateFormFile("source", "src.tar.gz")
		if err != nil {
			t.Fatalf("multipart CreateFormFile: %v", err)
		}
		if _, err := srcPart.Write(funcTar); err != nil {
			t.Fatalf("multipart Write source: %v", err)
		}
		// Issue #737 / ADR-083 wire shape. The CLI's auto-detect
		// path emits these exact field names; the apid function
		// validator at deploy_inputs.go:144-160 requires both to
		// be set on a function-typed app.
		if err := mw.WriteField("runtime", "node22"); err != nil {
			t.Fatalf("multipart WriteField runtime: %v", err)
		}
		if err := mw.WriteField("handler", "handler.handler"); err != nil {
			t.Fatalf("multipart WriteField handler: %v", err)
		}
		if err := mw.Close(); err != nil {
			t.Fatalf("multipart Close: %v", err)
		}
		req, err := http.NewRequest(http.MethodPost,
			fmt.Sprintf("%s/v1/apps/srcfunc/deployments", h.APIDURL), &body)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := h.HTTPClient().Do(req)
		if err != nil {
			t.Fatalf("function deploy POST: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("function deploy status=%d body=%s", resp.StatusCode, raw)
		}

		// Decode the deployment ID and assert the row is
		// function-typed (NOT app) — pins that the CLI's
		// Type="function" propagated through CreateApp into the
		// apps table.
		var respEnv struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &respEnv); err != nil {
			t.Fatalf("decode function deploy response: %v body=%s", err, raw)
		}
		if respEnv.ID == "" {
			t.Fatalf("function deploy response missing id; body=%s", raw)
		}
		var appType string
		if err := pool.QueryRow(context.Background(),
			`select type::text from apps where id = $1`, funcAppID,
		).Scan(&appType); err != nil {
			t.Fatalf("query app type: %v", err)
		}
		if appType != "function" {
			t.Errorf("app.type = %q, want %q (CLI's Type=function must reach the row)", appType, "function")
		}
		t.Logf("auto-pick-function-from-handler-js: dep=%s app_type=%s", respEnv.ID, appType)
	})
}

// fixedIdempotencyKey returns a stable UUID-shaped string used as the
// Idempotency-Key for subtest 8's replay. Stable (not time-based) so the
// test is deterministic across runs against the same schema; the apid
// idempotency table is keyed on (account_id, key) with a 24h TTL, so a
// fresh key per run is correct.
func fixedIdempotencyKey() string {
	return "11111111-2222-3333-4444-555555555555"
}

// doGetWithHostCapturingWakeID fires GET url with Host header host and
// returns body, status, and the x-faas-wake-id response header. Returns
// wakeID="" when the header is absent (e.g., the request hit a hot
// instance — caller decides whether that's a failure).
//
// Mirrors doGetWithHost (deploy_wake_metal_test.go:372-388) but threads
// the wake-id header back out so subtests 3, 6, and 7 can assert on the
// authoritative wake method.
func doGetWithHostCapturingWakeID(t *testing.T, client *http.Client, url, host string, timeout time.Duration) ([]byte, string, int) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Host = host
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("GET %s (Host=%s): %v", url, host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.Header.Get("x-faas-wake-id"), resp.StatusCode
}

// postMultipartCapturingHeaders is the subtest-8-only multipart helper
// that returns the Idempotent-Replayed response header alongside the
// status and body. postMultipartDeployment swallows headers because
// none of its existing callers need them; this sibling does the same
// multipart wiring but reads the full response so the test can assert
// on apid's idempotency middleware behavior.
func postMultipartCapturingHeaders(t *testing.T, h *e2etest.Harness, bearer, slug string, sourceTar []byte, isDockerfile bool, idempotencyKey string) ([]byte, int, bool) {
	t.Helper()
	// We can't share postMultipartDeployment's body-buffer logic without
	// refactoring it to return a *http.Response, so duplicate the small
	// multipart wiring here. Keep it short — if this helper grows past
	// ~30 lines, lift it into cmd/e2e/test_helpers.go alongside the
	// other shared helpers.
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
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Idempotency-Key", idempotencyKey)
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("deploy POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, resp.Header.Get("Idempotent-Replayed") == "true"
}

// instanceIDFromEvent pulls the `instance_id` field out of a
// wake.boot_completed event's decoded data payload. Used for diagnostic
// logging (the timeline JSON carries wake_id / instance_id / method).
func instanceIDFromEvent(ev state.Event) string {
	var p struct {
		InstanceID string `json:"instance_id"`
	}
	_ = json.Unmarshal(ev.Data, &p)
	return p.InstanceID
}

// writeGarbage overwrites the first n bytes of path with non-zero
// garbage so Firecracker's snapshot loader fails its magic-number /
// version check on the very first read. n is clamped to the file's
// current size (the corruption must happen INSIDE the existing file —
// lengthening the file would change Firecracker's expected file
// layout, and shortening to less than the header size would just trip
// the OS-layer read; we want a parse error from the FC loader, not
// a syscall error).
//
// Determinism: we write a fixed 0xFA byte pattern — distinct enough
// from any real FC snapshot header to fail every magic check, and
// reproducible across CI runs.
func writeGarbage(path string, n int64) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if n > st.Size() {
		n = st.Size()
	}
	if n == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 0xFA
	}
	if _, err := f.WriteAt(buf, 0); err != nil {
		return err
	}
	return f.Sync()
}
