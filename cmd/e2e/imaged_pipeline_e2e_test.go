// imaged_pipeline_e2e_test.go — the artifact pipeline, in a gate that runs.
//
// imaged owns `deployments.status` (spec §6.0): it drives imaging →
// snapshotting and it is the component that calls SetDeploymentFailed when a
// pull, scan, signature, or publish step rejects an image. None of that was
// covered by a CI gate. Until this file, imaged was booted by ZERO non-metal
// e2e tests, and 0 of its 49 unit-test files use a real Postgres — so every
// transition it owns was exercised against MemStore only, by a daemon that
// never ran.
//
// That is the shape of the #1666 incident (MemStore right, PgStore's SQL
// wrong, no test ran both), sitting on the deploy pipeline. The harness
// already knew how to start imaged; only the metal tests, which have never
// passed, ever asked it to.
//
// Scope, deliberately: this covers the control-plane state machine and the SQL
// under it, not ext4 mechanics. Real mkfs is already covered by
// pkg/rootfs/build_integration_test.go. No KVM is required — imaged needs vmmd
// only for parent-base staging, which is guarded by a nil check.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

type imagedFixture struct {
	h        *e2etest.Harness
	store    *state.PgStore
	registry *e2etest.FakeRegistry
	key      string
	ctx      context.Context
}

// newImagedFixture boots apid + imaged against a local registry.
//
// schedd is deliberately absent. imaged finishes its own work by publishing the
// layer and notifying for a snapshot prime; who consumes that notification is
// schedd's concern and needs a VM. Leaving it out keeps the test on the
// transitions imaged actually owns, and keeps it KVM-free.
func newImagedFixture(t *testing.T) *imagedFixture {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return nil
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The builder base is NOT stubbable. imaged validates the staged ext4 and
	// requires railpack, buildctl, runc and guest-init inside it
	// (pkg/e2etest/builderbase.go), so pointing imaged at a one-layer stub
	// makes it exit at boot — which is exactly what happened here first.
	//
	// A CI runner has no real base, so use the boot-contract recipe instead:
	// pre-provision the staged base and its digest sidecar, then point
	// FAAS_BUILDER_BASE_REF at a ref the registry will not serve. imaged takes
	// its normal registry-outage fallback and accepts what is already staged.
	storageRoot := t.TempDir()
	t.Setenv("FAAS_STORAGE_BACKEND", "local")
	t.Setenv("FAAS_STORAGE_ROOT", storageRoot)

	// The sidecar records guest-init's sha256, so the file imaged is told to
	// use and the file we hash must be the same one.
	guestInitBody := []byte("#!/bin/sh\n")
	guestInit := filepath.Join(t.TempDir(), "faas-guest-init")
	if err := os.WriteFile(guestInit, guestInitBody, 0o755); err != nil {
		t.Fatalf("write guest-init: %v", err)
	}
	t.Setenv("FAAS_GUEST_INIT", guestInit)
	seedBootContractBuilderBase(t, storageRoot, guestInitBody)

	// imaged validates an existing base read-only through debugfs. The
	// pipeline is what is under test, not ext4 mechanics, so shim it rather
	// than construct a multi-gigabyte filesystem on every CI run — the same
	// trade the boot-contract job makes.
	shimDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(shimDir, "debugfs"), []byte("#!/bin/sh\necho 'Inode: 1'\n"), 0o755); err != nil {
		t.Fatalf("write debugfs shim: %v", err)
	}
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)

	// The deploy-time base is a different repo whose single layer matches the
	// app image's lower layer, so oci.LayersAboveBase treats it as a prefix and
	// the app's own layer lands in `above` — the two-drive shape (§4.6).
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", imagedHelloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	deployBaseRef := registry.Host() + "/onebox-faas/deploy-base:latest"

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Imaged, []string{
		"FAAS_STORAGE_BACKEND=local",
		"FAAS_STORAGE_ROOT=" + storageRoot,
		// Digest-pinned (imaged refuses a tag and exits) and unservable, so the
		// staged base above is what imaged falls back to.
		"FAAS_BUILDER_BASE_REF=" + registry.Host() + "/onebox-faas/builder-base@sha256:" + repeatChar("0", 64),
		// Passed directly rather than through OverrideDeployBase, which
		// deliberately no-ops once a host "has a real base" — and the staged
		// fixture above makes that true. Its caution is aimed at clobbering a
		// genuine base on an acceptance node; here the base IS the fixture, so
		// the runtime base has to be redirected too or imaged reaches for the
		// production ghcr ref and 403s.
		"FAAS_TEST_DEPLOY_BASE_REF=" + deployBaseRef,
	})
	ctx := context.Background()
	return &imagedFixture{
		h:        h,
		store:    state.NewPgStore(pool),
		registry: registry,
		key:      h.SeedAccount(ctx, api.PlanHobby, "imaged-pipeline"),
		ctx:      ctx,
	}
}

const imagedHelloBody = "hello from the imaged pipeline\n"

// deployImage creates an app and posts a deployment for ref, returning the
// deployment id apid minted.
func (f *imagedFixture) deployImage(t *testing.T, slug, ref string) string {
	t.Helper()
	falsy := false
	body, status := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: &falsy})
	if status != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", status, body)
	}
	body, status = doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+slug+"/deployments",
		api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, body)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deployment: %v body=%s", err, body)
	}
	return dep.ID
}

// awaitDeployment polls the deployment row until cond holds, then returns it.
// Reads go through PgStore because the SQL is the thing under test.
func (f *imagedFixture) awaitDeployment(t *testing.T, id string, timeout time.Duration, cond func(state.Deployment) bool, what string) state.Deployment {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last state.Deployment
	for time.Now().Before(deadline) {
		dep, err := f.store.DeploymentByID(f.ctx, id)
		if err == nil {
			last = dep
			if cond(dep) {
				return dep
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("deployment %s never %s within %s; last status=%q error=%q rootfs=%q",
		id, what, timeout, last.Status, last.Error, last.RootfsPath)
	return last
}

// TestE2E_Imaged_PublishesLayerForAValidImage pins the imaging transition that
// every deploy depends on: imaged pulls the image, derives the per-app layer
// over the shared base, and records it on the deployment row.
//
// The rootfs assertion is the load-bearing one. §6.2-3 requires an app to have
// a live snapshot OR a cold-bootable rootfs, and this is the moment the second
// half comes into existence. A deployment that advances without one is an app
// that can never be woken.
func TestE2E_Imaged_PublishesLayerForAValidImage(t *testing.T) {
	f := newImagedFixture(t)
	if f == nil {
		return
	}
	img, _ := e2etest.HelloImageAboveBase("library/imaged-ok", imagedHelloBody)
	ref := f.registry.AddImage("library/imaged-ok", img)

	id := f.deployImage(t, "imaged-ok", ref)
	dep := f.awaitDeployment(t, id, 3*time.Minute, func(d state.Deployment) bool {
		return d.RootfsPath != "" || d.Status == state.DeployFailed
	}, "published a rootfs")

	if dep.Status == state.DeployFailed {
		t.Fatalf("imaging failed for a valid image: error=%q", dep.Error)
	}
	if dep.RootfsPath == "" {
		t.Error("deployment advanced without a cold-bootable rootfs (§6.2-3)")
	}
	if dep.Status == state.DeployPending {
		t.Errorf("status is still %q after the layer was published; imaged owns this transition (spec §6.0)", dep.Status)
	}
}

// TestE2E_Imaged_FailsClosedOnAnUnpullableImage pins the imaging → failed edge.
//
// It matters more than it looks. The alternative to a classified failure is a
// deployment that sits in `pending` forever — which is exactly how the
// FAAS_FUNCTION_RUNNER_* outage presented (PR #1286): every deploy stuck, no
// terminal state, and nothing that read as an error. A failed deploy must be
// legible as failed, and it must carry a reason a customer can act on.
//
// It also pins the safety property: no VM is ever started from an image that
// could not be verified, so a deployment that fails here must not have a
// rootfs recorded.
func TestE2E_Imaged_FailsClosedOnAnUnpullableImage(t *testing.T) {
	f := newImagedFixture(t)
	if f == nil {
		return
	}
	// A digest-shaped ref the registry will not serve. The pull is the first
	// step that can reject an image, and it is the one a customer hits by
	// typo or by deleting a tag out from under a deploy.
	ref := f.registry.Host() + "/library/does-not-exist@sha256:" + repeatChar("a", 64)

	id := f.deployImage(t, "imaged-missing", ref)
	dep := f.awaitDeployment(t, id, 3*time.Minute, func(d state.Deployment) bool {
		return d.Status == state.DeployFailed
	}, "reached failed")

	if dep.Error == "" {
		t.Error("failed deployment carries no error; a customer cannot act on a bare status")
	}
	if dep.RootfsPath != "" {
		t.Errorf("failed deployment recorded a rootfs %q; no artifact may be published from an unverified image", dep.RootfsPath)
	}
}
