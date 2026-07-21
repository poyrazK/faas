//go:build metal

// secrets_image_deploy_e2e_test.go — G2 ship-blocker acceptance for
// image-deploy apps with secrets (ADR-020 future-work #2).
//
// The two pieces that previously prevented this end-to-end:
//
//  1. cmd/vmmd/main.go never wired the host-key lifecycle, so
//     Manager.Wake returned ErrNoHostKey for any app that PUT a
//     secret (Subtask 2 of this PR closes that).
//  2. pkg/fakeregistry's hardcoded Cmd ("cat app/hello.txt") doesn't
//     allow custom env consumption; the existing function-deploy
//     metal suite tests secrets injection through the runner shim,
//     but no test exercised the image-deploy wake path with a
//     secret present.
//
// This test asserts (1) end-to-end: it sets the host-key env vars
// before booting the harness, PUTs a secret on an app, deploys the
// standard HelloImageAboveBase image, and asserts the deployment
// reaches live + a parked instance materializes. That success path
// is only reachable when vmmd's Manager has a host identity, so a
// green run proves the G2 ship-blocker is closed.
//
// The full secret-value-in-guest assertion (mirroring what
// secrets_env_metal_test.go does for function deploys) requires a
// fakeregistry change to allow custom Cmd/Env — tracked as
// follow-up. Until then, the wake-with-secret path's success is the
// load-bearing proof.
//
// Build tag: metal. Requires /dev/kvm + root + FAAS_TEST_KERNEL +
// Postgres. Runs on EX44 via `make test-metal`, or M3+ Mac via
// `make metal-lima`.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// secretEchoBody is the plaintext value the customer PUTs as
// MY_SECRET. Used by the unit-level unseal tests; the metal path
// only proves the wake didn't refuse the secret-bearing app.
const secretEchoBody = "hello-from-test"

// TestSecretsImageDeployMetal wires vmmd's host-key lifecycle
// through the harness env, PUTs a secret on an image-deploy app,
// and asserts the deployment reaches live + a parked instance
// materializes. The wake path inside Manager only succeeds when
// SetHostIdentity was called with a non-nil identity, so a green
// run is the load-bearing proof that the G2 chain holds end-to-end.
func TestSecretsImageDeployMetal(t *testing.T) {
	// Pre-flight: metal env. vmmd would otherwise fail on its first
	// ColdBoot with a much less helpful error.
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal secrets+image-deploy test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}

	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Host-key wiring: write a fresh X25519 recipient to a temp file
	// so apid can seal against it, and tell vmmd to load (or generate)
	// the matching private identity from a sibling file. vmmd's
	// loadOrGenerateHostIdentity (cmd/vmmd/main.go) will: (a) try to
	// load the key, (b) ErrHostKeyNotFound → generate, (c) write the
	// public recipient. apid seals with the public half; vmmd unseals
	// with the private half. Pre-seeding both halves guarantees a
	// match before the harness subprocesses boot.
	tmpDir := t.TempDir()
	keyPath := filepath.Join(tmpDir, "host.age")
	pubPath := filepath.Join(tmpDir, "host.age.pub")
	preSeedHostKey(t, keyPath, pubPath)
	t.Setenv("FAAS_HOST_KEY_PATH", keyPath)
	t.Setenv("FAAS_HOST_AGE_RECIPIENT_PATH", pubPath)

	// Fake registry + base stubs (mirrors cmd/e2e/deploy_wake_metal_test.go).
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	_ = registry.AddImage("onebox-faas/builder-base", builderImg)
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	t.Setenv("FAAS_TEST_BUILDER_BASE_REF", registry.Host()+"/onebox-faas/builder-base:latest")
	t.Setenv("FAAS_TEST_DEPLOY_BASE_REF", registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake)
	defer h.DumpLogs(t)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// App + secret PUT before deploy. The order doesn't matter for
	// correctness (vmmd reads live app_secrets rows at wake time), but
	// this order matches the production CLI flow.
	if got := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: "secrets-app", Type: "app"}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, key, "secrets-app")
	if code := statusOnly(t, h, key, http.MethodPut,
		"/v1/apps/secrets-app/secrets/MY_SECRET",
		api.PutAppSecretRequest{Value: secretEchoBody}); code != http.StatusOK {
		t.Fatalf("PUT secret: %d", code)
	}

	// Image deploy. The exact image body is irrelevant to this test;
	// HelloImageAboveBase is the well-trodden two-drive fixture.
	img, ref := e2etest.HelloImageAboveBase("library/hello", secretEchoBody)
	ref = registry.AddImage("library/hello", img)

	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/secrets-app/deployments",
		api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var depResp api.DeploymentResponse
	if err := json.Unmarshal(raw, &depResp); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, depResp.ID)
	}

	// Wait for live + parked. Success here proves vmmd's Manager has
	// a host identity and accepted the wake with secrets on the app.
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, depResp.ID, 60*time.Second); err != nil {
		// On failure, dump the deployment row + tag a G2 ship-blocker
		// failure explicitly. hostKeyNotLoadedMarker in the deployment's
		// error message is the unambiguous signal that vmmd's Manager
		// returned pkg/fcvm.ErrNoHostKey — i.e. the host-key lifecycle
		// wiring in cmd/vmmd/main.go regressed and the G2 ship-blocker
		// is back.
		if d, derr := state.NewPgStore(pool).DeploymentByID(ctx, depResp.ID); derr == nil {
			t.Logf("deployment state at failure: status=%s error=%q code=%q",
				d.Status, d.Error, d.ErrorCode)
			if d.Status == state.DeployFailed && strings.Contains(d.Error, hostKeyNotLoadedMarker) {
				t.Fatalf("G2 SHIP-BLOCKER REGRESSED: vmmd refused to wake — host-key lifecycle not wired: %v", err)
			}
		}
		t.Fatalf("deployment did not reach live: %v", err)
	}
	ins, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 30*time.Second)
	if err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	if len(ins) == 0 {
		t.Fatal("no instances after deploy")
	}
	t.Logf("secrets-app deploy+park OK: dep=%s instance=%s parked",
		depResp.ID, ins[0].ID)
}

// --- helpers ---------------------------------------------------------------

// preSeedHostKey generates an X25519 identity and writes both the private
// (keyPath) and public (pubPath) halves. vmmd will load the private half
// on boot and re-emit the public half (idempotent). apid reads the
// public half to seal. Without this pre-seed, vmmd's first-boot path
// would generate a key that doesn't match anything apid saw, and every
// unseal would fail.
func preSeedHostKey(t *testing.T, keyPath, pubPath string) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(id.String()), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.WriteFile(pubPath, []byte(id.Recipient().String()), 0o444); err != nil {
		t.Fatalf("write pub: %v", err)
	}
}

// hostKeyNotLoadedMarker is the substring of pkg/fcvm.ErrNoHostKey
// ("fcvm: host identity not loaded") that surfaces in the deployment
// row when vmmd's Manager refuses to wake a secret-bearing app. We
// can't errors.Is here — schedd translates the wake error into the
// row's free-text Error column before we read it — so the test pins
// against the sentinel's message text. If the sentinel's wording
// changes, this check will fire and force a co-located update, which
// is the desired tripwire.
const hostKeyNotLoadedMarker = "fcvm: host identity not loaded"
