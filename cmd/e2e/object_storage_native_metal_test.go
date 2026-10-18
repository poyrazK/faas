//go:build metal && object_storage_native

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/storagestarter"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestObjectStorageNativeMetal deploys the unchanged embedded application in
// real builder/serving guests. Only provider storage and the gateway's TLS edge
// are test-owned; signing, catalog custody, scheduling and restore are real.
func TestObjectStorageNativeMetal(t *testing.T) {
	storageNativePrerequisites(t)
	for _, starter := range storagestarter.SuiteContract().Starters {
		t.Run(starter.Template, func(t *testing.T) { runStorageNativeJourney(t, starter) })
	}
}

func runStorageNativeJourney(t *testing.T, starter storagestarter.Starter) {
	t.Helper()
	var journey *storageNativeJourney
	var verified bool
	// Emit identity last, after every registered fixture/daemon/catalog cleanup.
	t.Cleanup(func() {
		if t.Failed() || !verified {
			return
		}
		encoded, err := json.Marshal(journey.manifest)
		if err != nil {
			t.Error("storage acceptance identity encoding failed")
			return
		}
		t.Log(storagestarter.IdentityPrefix + string(encoded))
	})
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Fatal("storage native acceptance did not open its disposable catalog")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	journey = newStorageNativeJourney(t, pool, starter)
	phases := map[string]func(*testing.T){
		"starter_deploy_and_initial_restore": journey.deploy,
		"same_revision_transport_recovery":   journey.restore,
		"running_rotation_and_retirement":    func(t *testing.T) { journey.rotate(t, false) },
		"parked_rotation_scheduler_recovery": func(t *testing.T) { journey.rotate(t, true) },
		"rotated_snapshot_restore":           journey.restore,
		"failed_hook_cold_boot":              journey.failedHook,
		"public_hook_forbidden":              journey.publicHook,
		"revoke_and_independent_cleanup":     journey.cleanup,
	}
	contract := storagestarter.SuiteContract()
	if len(phases) != len(contract.Phases) {
		t.Fatal("storage native phase contract mismatch")
	}
	for _, name := range contract.Phases {
		phase := phases[name]
		if phase == nil || !t.Run(name, phase) {
			t.FailNow()
		}
	}
	manifest, err := e2etest.StorageGuestRuntimeManifest(journey.artifacts, journey.manifest.SourceRevision, starter.Template, true)
	if err != nil || !reflect.DeepEqual(manifest, journey.manifest) {
		t.Fatal("storage acceptance artifacts changed during the journey")
	}
	verified = true
}

func (j *storageNativeJourney) deploy(t *testing.T) {
	base := "/v1/apps/" + j.slug
	data, status := doReq(t, j.h, j.key, http.MethodPost, base+"/buckets", api.CreateObjectBucketRequest{Name: "assets"})
	if status != http.StatusCreated || json.Unmarshal(data, &j.bucket) != nil || j.bucket.State != "ready" {
		t.Fatal("public API did not create a ready storage bucket")
	}
	// Seed only disposable provider accounting, as in portable storage acceptance.
	// Runtime delivery/adoption and rotation receipts are never manufactured.
	seedObjectStorageAccounting(t, objectStorageE2EEnv{pool: j.pool, backend: j.backend}, j.account, j.appID, j.bucket.ID)
	data, status = doReq(t, j.h, j.key, http.MethodPost, j.bindingBase()+"/compute-bindings", api.CreateObjectStorageComputeBindingRequest{
		Label: "native-starter", Permission: api.ObjectBucketPermissionReadWrite, Prefix: "GREGALE_S3_ASSETS"})
	if status != http.StatusCreated || json.Unmarshal(data, &j.binding) != nil || j.binding.ID == "" {
		t.Fatal("public API did not create storage compute binding")
	}
	if status := statusOnly(t, j.h, j.key, http.MethodPut, base+"/secrets/STORAGE_APP_TOKEN", api.PutAppSecretRequest{Value: j.token}); status != http.StatusOK {
		t.Fatalf("create private HTTP application token: status=%d", status)
	}
	source, err := storagestarter.Source(j.starter.Template, j.gateway.CA)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, source)
	if err := os.WriteFile(j.sourcePath, archive, 0600); err != nil {
		t.Fatal("could not retain exact starter source for evidence")
	}
	data, status = postMultipartDeployment(t, j.h, j.key, j.slug, archive, true, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy unchanged storage starter: status=%d", status)
	}
	j.deployment, _ = parseQueuedDeployment(t, data)
	ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, j.pool, j.deployment, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("storage source deployment: %v", err)
	}
	j.recordRuntime(t)
	app, err := j.store.AppByID(ctx, j.appID)
	if err != nil || app.Manifest.AfterRestore == nil || app.Manifest.AfterRestore.Path != "/internal/after-restore" || app.Manifest.AfterRestore.TimeoutMS != 2000 {
		t.Fatal("shipped storage lifecycle manifest was not applied")
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, j.pool, j.appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	j.snapshot = waitAfterRestoreSnapshot(t, j.store, j.deployment, "")
	j.request(t, http.MethodGet, "readyz", nil, http.StatusOK, "restore")
	j.waitForAdoption(t)
	j.write(t, "initial.bin", bytes.Repeat([]byte{0, 1, 127, 128, 255}, 3277))
	j.readAll(t, "")
}

// These Dockerfile starters run as full OCI apps. Their interpreters live in
// drive1, not a published function-runtime release. Bind the actual app layer
// and exact source archive to builder provenance, alongside the shared drive0.
func (j *storageNativeJourney) recordRuntime(t *testing.T) {
	t.Helper()
	app, err := j.store.AppByID(t.Context(), j.appID)
	if err != nil {
		t.Fatal("storage application identity missing")
	}
	deployment, err := j.store.DeploymentByID(t.Context(), j.deployment)
	if err != nil {
		t.Fatal("storage deployment OCI artifact identity missing")
	}
	build, err := j.store.BuildByDeployment(t.Context(), j.deployment)
	if err != nil {
		t.Fatal("storage source build did not complete")
	}
	provenance, err := j.store.BuildProvenanceByBuildID(t.Context(), build.ID)
	if err != nil {
		t.Fatal("storage source build provenance missing")
	}
	root := os.Getenv("FAAS_STORAGE_ROOT")
	if root == "" {
		root = "/srv/fc"
	}
	paths, err := e2etest.StorageGuestAppArtifacts(j.starter.Template, root, j.sourcePath, app, deployment, build, provenance)
	if err != nil {
		t.Fatal(err)
	}
	for name, path := range paths {
		digest, err := e2etest.PostgresGuestArtifactDigest(path)
		if err != nil {
			t.Fatal("storage deployed artifact bytes unverified")
		}
		j.artifacts[name], j.manifest.Artifacts[name] = path, digest
	}
}

func (j *storageNativeJourney) restore(t *testing.T) {
	before := j.secretVersions(t)
	j.readAll(t, "") // retain actual signed S3 connections in the captured app
	connection := j.lastListConnection(t)
	j.park(t)
	j.gateway.Disconnect()
	j.readAll(t, "restore")
	if next := j.lastListConnection(t); next == 0 || next == connection {
		t.Fatal("restored storage starter did not validate a fresh TLS connection")
	}
	if !reflect.DeepEqual(before, j.secretVersions(t)) {
		t.Fatal("transport recovery unexpectedly changed secret versions")
	}
	j.waitForAdoption(t)
	j.verifyProvider(t)
}

func (j *storageNativeJourney) park(t *testing.T) {
	t.Helper()
	current, err := j.store.LatestSnapshotForTier(t.Context(), j.deployment, state.SnapshotTierInit)
	previous := j.snapshot
	if err == nil {
		previous = current.ID
		if err := j.store.MarkSnapshotStale(t.Context(), current.ID); err != nil {
			t.Fatal(err)
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		t.Fatal("could not inspect storage snapshot before fresh capture")
	}
	// A rolling credential refresh cold-boots without taking a snapshot. When
	// its prior captures are already stale, this park creates the next init.
	if status := statusOnly(t, j.h, j.key, http.MethodPost, "/v1/apps/"+j.slug+"/park", nil); status != http.StatusNoContent {
		t.Fatalf("park storage starter: status=%d", status)
	}
	j.snapshot = waitAfterRestoreSnapshot(t, j.store, j.deployment, previous)
	if _, err := e2etest.WaitForInstanceState(t.Context(), t, j.pool, j.appID, state.StateParked, 60*time.Second); err != nil {
		t.Fatal(err)
	}
}

func (j *storageNativeJourney) rotate(t *testing.T, parked bool) {
	old := j.credential(t)
	j.readAll(t, "")
	if parked {
		j.park(t)
		if err := j.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
	}
	instances, err := j.store.ListInstancesForApp(t.Context(), j.appID)
	if err != nil {
		t.Fatal(err)
	}
	data, status := doReq(t, j.h, j.key, http.MethodPost, j.bindingBase()+"/compute-bindings/"+j.binding.ID+"/rotate", nil)
	var rotated api.ObjectStorageComputeBinding
	if status != http.StatusOK || json.Unmarshal(data, &rotated) != nil || rotated.ID != j.binding.ID || rotated.Credential.AccessKeyID == old.accessKey || !rotated.RotationPending {
		t.Fatal("public API did not queue storage credential rotation")
	}
	j.requireStale(t, j.snapshot)
	if parked {
		j.signedRead(t, old, "", http.StatusOK)
		if err := j.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
	}
	storageNativeWait(t, "public storage rotation completion", func(ctx context.Context) bool {
		data, status, err := j.apiGet(ctx, j.bindingBase()+"/compute-bindings")
		var list api.ObjectStorageComputeBindingList
		if err != nil || status != http.StatusOK || json.Unmarshal(data, &list) != nil {
			return false
		}
		for _, binding := range list.Items {
			if binding.ID == j.binding.ID && binding.Credential.AccessKeyID == rotated.Credential.AccessKeyID && !binding.RotationPending {
				j.binding = binding
				return true
			}
		}
		return false
	})
	for _, previous := range instances {
		if !state.State(previous.State).CountsForRAM() {
			continue
		}
		current, err := j.store.InstanceByID(t.Context(), previous.ID)
		if err != nil || state.State(current.State).CountsForRAM() {
			t.Fatal("storage rotation completed before previous resident guest retired")
		}
	}
	j.signedRead(t, old, "", http.StatusForbidden)
	j.signedRead(t, j.credential(t), "", http.StatusOK)
	j.readAll(t, "")
	j.waitForAdoption(t)
	name := "running-rotation.bin"
	if parked {
		name = "parked-rotation.bin"
	}
	j.write(t, name, []byte("new credential write: "+name))
	j.readAll(t, "")
	j.verifyProvider(t)
}

func (j *storageNativeJourney) failedHook(t *testing.T) {
	j.park(t)
	failed := j.snapshot
	j.gateway.Disconnect()
	fault, err := j.gateway.DelayNextList(2500 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer j.gateway.ClearFault()
	ackBefore := strings.Count(j.h.VmmdLogs(), "application after_restore failed (ack=13)")
	j.readAll(t, "cold_boot")
	if strings.Count(j.h.VmmdLogs(), "application after_restore failed (ack=13)") <= ackBefore {
		t.Fatal("storage cold boot did not follow failed application recovery")
	}
	for name, signal := range map[string]<-chan struct{}{"entered": fault.Entered, "cancelled": fault.Cancelled, "finished": fault.Done} {
		select {
		case <-signal:
		case <-time.After(5 * time.Second):
			t.Fatalf("restore validation fault was not %s", name)
		}
	}
	j.requireStale(t, failed)
	instances, err := j.store.ListInstancesForApp(t.Context(), j.appID)
	if err != nil {
		t.Fatal(err)
	}
	residents := 0
	for _, instance := range instances {
		if state.State(instance.State).CountsForRAM() {
			residents++
		}
	}
	if residents != 1 {
		t.Fatalf("failed storage restore left %d resident guests; want one", residents)
	}
	j.waitForAdoption(t)
	j.verifyProvider(t)
}

func (j *storageNativeJourney) publicHook(t *testing.T) {
	j.request(t, http.MethodPost, "internal/after-restore", nil, http.StatusForbidden, "")
	j.readAll(t, "")
}

func (j *storageNativeJourney) cleanup(t *testing.T) {
	j.verifyProvider(t)
	storedBucket, err := j.store.GetObjectBucket(t.Context(), j.account, j.appID, j.bucket.ID)
	if err != nil {
		t.Fatal("cleanup could not observe the owned provider bucket")
	}
	old := j.credential(t)
	data, status := doReq(t, j.h, j.key, http.MethodPost, j.bindingBase()+"/s3-credentials", api.CreateObjectS3CredentialRequest{
		Label: "independent-cleanup-control", Permission: api.ObjectBucketPermissionReadWrite})
	var control api.ObjectS3CredentialSecret
	if status != http.StatusCreated || json.Unmarshal(data, &control) != nil || control.ID == "" {
		t.Fatal("could not create independent cleanup control")
	}
	j.controlID = control.ID
	if status := statusOnly(t, j.h, j.key, http.MethodDelete, j.bindingBase()+"/compute-bindings/"+j.binding.ID, nil); status != http.StatusNoContent {
		t.Fatalf("delete owned storage binding: status=%d", status)
	}
	j.signedRead(t, old, "", http.StatusForbidden)
	controlCredential := storageNativeCredential{accessKey: control.AccessKeyID, secret: control.SecretAccessKey}
	j.signedRead(t, controlCredential, "", http.StatusOK)
	if status := statusOnly(t, j.h, j.key, http.MethodDelete, j.bindingBase()+"/s3-credentials/"+control.ID, nil); status != http.StatusNoContent {
		t.Fatalf("revoke independent cleanup control: status=%d", status)
	}
	j.signedRead(t, controlCredential, "", http.StatusForbidden)
	rows, err := j.store.ListAppSecretsInScope(t.Context(), j.account, j.appID, api.DefaultEnvScope)
	if err != nil || len(rows) != 1 || rows[0].Key != "STORAGE_APP_TOKEN" {
		t.Fatal("binding deletion retained managed runtime secrets")
	}
	var active int
	if err := j.pool.QueryRow(t.Context(), `SELECT count(*) FROM object_storage_s3_credentials WHERE account_id=$1 AND bucket_id=$2 AND status='active'`, j.account, j.bucket.ID).Scan(&active); err != nil || active != 0 {
		t.Fatal("binding cleanup retained active credentials or hidden rotation stages")
	}
	for key := range j.expected {
		if status := statusOnly(t, j.h, j.key, http.MethodDelete, j.bindingBase()+"/objects?key="+key, nil); status != http.StatusNoContent {
			t.Fatalf("delete owned object: status=%d", status)
		}
	}
	if status := statusOnly(t, j.h, j.key, http.MethodDelete, j.bindingBase(), nil); status != http.StatusNoContent {
		t.Fatalf("delete owned bucket: status=%d", status)
	}
	j.provider.mu.Lock()
	_, retained := j.provider.buckets[storedBucket.PhysicalName]
	j.provider.mu.Unlock()
	if retained {
		t.Fatal("public bucket deletion retained provider resources")
	}
	if status := statusOnly(t, j.h, j.key, http.MethodDelete, "/v1/apps/"+j.slug+"/secrets/STORAGE_APP_TOKEN", nil); status != http.StatusNoContent {
		t.Fatalf("delete owned application token: status=%d", status)
	}
	rows, err = j.store.ListAppSecretsInScope(t.Context(), j.account, j.appID, api.DefaultEnvScope)
	if err != nil || len(rows) != 0 {
		t.Fatal("storage cleanup retained runtime secret rows")
	}
	j.h.Stop()
	j.cleaned = true
}
