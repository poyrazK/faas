//go:build managed_postgres_native && metal

package e2e_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresprobe"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresstarter"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestManagedPostgresNativeMetal exercises real SQL in deployment release,
// serving, task, replacement and restored guests. Only provider management is
// simulated: the catalog, sealed credentials, daemons and SQL authority are real.
func TestManagedPostgresNativeMetal(t *testing.T) {
	var guestIdentity *guestqualification.Identity
	var artifactPaths map[string]string
	var artifactsVerified bool
	// Registered first: emit evidence only after every SQL/daemon cleanup has
	// succeeded, and recheck artifacts after the workload rather than trusting
	// a caller-supplied manifest or a reused harness binary directory.
	t.Cleanup(func() {
		if t.Failed() || guestIdentity == nil || !artifactsVerified {
			return
		}
		encoded, err := json.Marshal(guestIdentity)
		if err != nil {
			t.Error("guest qualification identity encoding failed")
			return
		}
		t.Log(guestqualification.IdentityPrefix + string(encoded))
	})
	if os.Getenv("GREGALE_POSTGRES_NATIVE_ACCEPTANCE") != "1" {
		t.Fatal("use make test-managed-postgres-native for isolated SQL guest acceptance")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("SQL guest acceptance requires root on x86_64 Linux")
	}
	for _, required := range []string{"DATABASE_URL", "FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "FAAS_GUEST_INIT", "GREGALE_POSTGRES_NATIVE_SQL_IP", "GREGALE_POSTGRES_NATIVE_CA_PATH"} {
		if os.Getenv(required) == "" {
			t.Fatalf("missing native SQL prerequisite %s", required)
		}
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("SQL guest acceptance cannot skip PostgreSQL")
	}
	if err := e2etest.ValidateNativeGuestInit(os.Getenv("FAAS_GUEST_INIT")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal("SQL guest acceptance requires KVM")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Fatal("SQL guest acceptance did not open its catalog")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	proxy := postgresfixture.OpenProxy(t, pool, os.Getenv("GREGALE_POSTGRES_NATIVE_SQL_IP"), 5432)
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	builder, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builder))
	base, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "postgres-native")
	_ = registry.AddImage("onebox-faas/deploy-base", base)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keys := t.TempDir()
	for _, name := range []string{"host.age", "fleet.age"} {
		if err := secretbox.WriteHostKeyAtPath(filepath.Join(keys, name), identity); err != nil {
			t.Fatal(err)
		}
	}
	public := filepath.Join(keys, "host.age.pub")
	if err := secretbox.WriteRecipientFile(public, identity); err != nil {
		t.Fatal(err)
	}
	hmac := make([]byte, 32)
	if _, err := rand.Read(hmac); err != nil {
		t.Fatal(err)
	}
	hmacPath := filepath.Join(keys, "host.hmac")
	if err := os.WriteFile(hmacPath, hmac, 0400); err != nil {
		t.Fatal(err)
	}
	fixture := postgresfixture.New(t, pool, identity, hmac, proxy.Host, proxy.Port)
	if fixture == nil {
		t.Fatal("customer SQL fixture missing")
	}
	environment := []string{
		"FAAS_HOST_HMAC_KEY_PATH=" + hmacPath,
		"FAAS_RELEASE_PHASE_ENABLED=1",
		// apid gates the public app task API separately from schedd dispatch.
		"FAAS_APP_TASK_API_ENABLED=1",
		"FAAS_APP_TASK_DISPATCH=1",
		"FAAS_HOST_KEY_PATH=" + filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_IDENTITY_PATH=" + filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + public,
		"FAAS_FLEET_AGE_RECIPIENT_PATH=" + public,
	}
	environment = append(environment, fixture.DaemonEnvironment(t)...)
	// The daemon harness deliberately inherits a narrow environment. Forward
	// the isolated scanner configuration explicitly; all scans remain required.
	for _, name := range []string{"GRYPE_DB_CACHE_DIR", "GRYPE_DB_AUTO_UPDATE"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	if root := os.Getenv("FAAS_STORAGE_ROOT"); root != "" {
		environment = append(environment, "FAAS_STORAGE_ROOT="+root)
	}
	h := e2etest.Start(t, pool, e2etest.All, environment...)
	revision, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal("guest qualification requires a checkout revision")
	}
	artifactPaths = map[string]string{"kernel": os.Getenv("FAAS_TEST_KERNEL"), "guest_init": os.Getenv("FAAS_GUEST_INIT"), "builder_base": e2etest.StagedBuilderBasePath()}
	for _, name := range append(append([]string(nil), e2etest.DaemonBinaries...), e2etest.StaticHelperBinaries...) {
		artifactPaths[name] = filepath.Join(h.BinDir, name)
	}
	for _, name := range []string{"firecracker", "jailer"} {
		artifactPaths[name], err = exec.LookPath(name)
		if err != nil {
			t.Fatal("guest qualification host executable missing")
		}
	}
	artifactPaths["e2e_test"], err = os.Executable()
	if err != nil {
		t.Fatal("guest qualification test executable missing")
	}
	manifest, err := e2etest.PostgresGuestRuntimeManifest(artifactPaths, strings.TrimSpace(string(revision)), false)
	if err != nil {
		t.Fatal(err)
	}
	guestIdentity = &guestqualification.Identity{Runtime: manifest, PostgresMajor: fixture.Major}
	key := h.SeedAccount(t.Context(), api.PlanPro)
	store := state.NewPgStore(pool)
	account := accountIDFromKey(t, t.Context(), pool, key)
	recordRuntime := func(t *testing.T, deploymentID, workload, sourceArchive string) {
		t.Helper()
		deployment, err := store.DeploymentByID(t.Context(), deploymentID)
		if err != nil {
			t.Fatal("guest deployment identity missing")
		}
		app, err := store.AppByID(t.Context(), deployment.AppID)
		if err != nil {
			t.Fatal("guest application identity missing")
		}
		build, err := store.BuildByDeployment(t.Context(), deploymentID)
		if err != nil {
			t.Fatal("guest source build missing")
		}
		provenance, err := store.BuildProvenanceByBuildID(t.Context(), build.ID)
		if err != nil {
			t.Fatal("guest source build provenance missing")
		}
		storageRoot := os.Getenv("FAAS_STORAGE_ROOT")
		if storageRoot == "" {
			storageRoot = "/srv/fc"
		}
		paths, err := e2etest.PostgresGuestAppArtifacts(workload, storageRoot, sourceArchive, app, deployment, build, provenance)
		if err != nil {
			t.Fatal(err)
		}
		for name, path := range paths {
			digest, err := e2etest.PostgresGuestArtifactDigest(path)
			if err != nil {
				t.Fatal("guest deployed artifact bytes unverified")
			}
			if previous := guestIdentity.Runtime.Artifacts[name]; previous != "" && previous != digest {
				t.Fatal("shared guest artifact changed between workloads")
			}
			artifactPaths[name], guestIdentity.Runtime.Artifacts[name] = path, digest
		}
	}

	slug := "postgres-native-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RAMMB: 256, RequireAuthn: &falsy}); status != http.StatusCreated {
		t.Fatalf("create SQL app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	ports := []int{5432}
	// ADR-373 requires resolved DNS or an explicit customer CIDR for a
	// literal endpoint. This isolated proxy uses only one benchmark IP.
	proxyCIDR := netip.PrefixFrom(netip.MustParseAddr(proxy.Host), 32).String()
	destinations := []string{proxyCIDR}
	// Pro apps default to public_auth_mode=bearer (ADR-080) as well as
	// require_authn; the SQL probe requests are anonymous, so open both gates.
	openAuth := &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen}
	if status := statusOnly(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{EgressPorts: &ports, EgressAllowlist: &destinations, PublicAuth: openAuth}); status != http.StatusOK {
		t.Fatalf("declare SQL destination and TCP port through app policy: status=%d", status)
	}
	app, err := store.AppByID(t.Context(), appID)
	if err != nil || len(app.EgressAllowlist) != 1 || app.EgressAllowlist[0].String() != proxyCIDR || len(app.EgressPorts) != 1 || app.EgressPorts[0] != 5432 {
		t.Fatal("public app policy did not persist the isolated SQL destination and port")
	}
	database, bindings := fixture.Create(t, account, appID)
	uri := func(binding mp.Binding) string {
		t.Helper()
		value, err := fixture.Observer.URI(t.Context(), binding)
		if err != nil {
			t.Fatal("independent credential observation failed")
		}
		return value
	}
	cleanup := func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
		h.Stop()
		fixture.Enabled = false
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		current, err := fixture.Catalog.ListBindings(ctx, account, database.ID)
		if err != nil {
			t.Error("cleanup could not inventory owned bindings")
			return
		}
		for _, binding := range current {
			if binding.State == mp.BindingStateDeleted {
				continue
			}
			value, observed := fixture.Observer.URI(ctx, binding)
			if _, err := fixture.Bindings.Delete(ctx, account, binding.ID); err != nil {
				t.Error("cleanup binding deletion failed")
				continue
			}
			if fixture.Observer.Deleted(ctx, binding) != nil || (observed == nil && fixture.VerifyRevoked(ctx, value) != nil) {
				t.Error("cleanup did not prove secret removal and SQL retirement")
			}
		}
		if _, err := fixture.Service.Delete(ctx, account, database.ID); err != nil {
			t.Error("cleanup database deletion failed")
		}
	}
	t.Cleanup(cleanup)
	runID := uuid.NewString()
	for name, value := range map[string]string{"POSTGRES_PROBE_RUN_ID": runID, "POSTGRES_PROBE_MAJOR": strconv.Itoa(fixture.Major)} {
		if status := statusOnly(t, h, key, http.MethodPut, "/v1/apps/"+slug+"/env/"+name, api.PutAppEnvRequest{Value: value}); status != http.StatusOK {
			t.Fatalf("set nonsecret probe config: status=%d", status)
		}
	}
	var deploymentID, initialSnapshot string
	var last postgresprobe.Result
	probe := func(t *testing.T, binding mp.Binding, wakeMethod string) postgresprobe.Result {
		t.Helper()
		body, wakeID, status := doGetWithHostCapturingWakeID(t, h.HTTPClient(), gatewayAppURL(h, slug)+"probe", slug+".apps.test.example", 45*time.Second)
		var result postgresprobe.Result
		if status != http.StatusOK || json.Unmarshal(body, &result) != nil {
			t.Fatalf("guest SQL workload failed: status=%d", status)
		}
		if result.RuntimeProof != postgresprobe.Proof(runID, uri(binding)) || result.ReaderProof != postgresprobe.Proof(runID, uri(bindings[mp.CredentialReadOnly])) || result.Counter <= last.Counter || (last.Marker != "" && result.Marker != last.Marker) {
			t.Fatal("guest received wrong credentials or lost SQL data")
		}
		if result.PoolGeneration == 0 || result.WriterConnections == 0 || result.ReaderConnections == 0 || (wakeMethod == "restore" && result.PoolGeneration < 2) {
			t.Fatal("SQL guest did not retain pools or complete its restore reconnect hook")
		}
		if wakeMethod != "" {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			if wakeID == "" {
				t.Fatal("SQL guest response missing wake receipt")
			}
			if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, wakeMethod, 10*time.Second); err != nil {
				t.Fatalf("SQL workload did not use %s: %v", wakeMethod, err)
			}
		}
		return result
	}
	phase := func(name string, fn func(*testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}
	probeSource := filepath.Join(t.TempDir(), "probe-source.tar.gz")
	phase("release_and_initial_restore", func(t *testing.T) {
		binary, err := e2etest.PostgresProbeExecutable()
		if err != nil {
			t.Fatal(err)
		}
		source := buildTarGz(t, map[string]string{
			"Dockerfile":       "FROM scratch\nCOPY --chmod=0755 postgres-probe /postgres-probe\nCOPY --chmod=0755 postgres-probe /bin/sh\nEXPOSE 8080\nCMD [\"/postgres-probe\"]\n",
			"postgres-probe":   string(binary),
			"gregale.yaml":     "hosting:\n  port: 8080\n  health: /healthz\nlifecycle:\n  after_restore:\n    path: /internal/after-restore\n    timeout_ms: 2000\nrelease:\n  command: /postgres-probe migrate\n",
			"faas-build-token": runID,
		})
		if err := os.WriteFile(probeSource, source, 0600); err != nil {
			t.Fatal("retain exact SQL probe source")
		}
		body, status := postMultipartDeployment(t, h, key, slug, source, true, "")
		if status != http.StatusAccepted {
			t.Fatalf("deploy SQL source: status=%d", status)
		}
		deploymentID, _ = parseQueuedDeployment(t, body)
		ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, deploymentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			if release, taskErr := store.ReleaseAppTaskByDeployment(ctx, deploymentID); taskErr == nil {
				t.Logf("SQL probe failure stage: %s", postgresprobe.DiagnosticStage(release.StderrTail))
			}
			t.Fatalf("SQL source deployment: %v", err)
		}
		recordRuntime(t, deploymentID, "probe", probeSource)
		release, err := store.ReleaseAppTaskByDeployment(ctx, deploymentID)
		if err != nil || release.Kind != state.AppTaskKindRelease || release.Status != state.AppTaskSucceeded {
			t.Fatal("normal deployment did not complete its SQL migration release")
		}
		currentApp, err := store.AppByID(ctx, appID)
		if err != nil || currentApp.Manifest.AfterRestore == nil || currentApp.Manifest.AfterRestore.Path != postgresprobe.RestorePath {
			t.Fatal("source deployment did not persist its pool reconnect hook")
		}
		if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
			t.Fatal("initial SQL guest snapshot did not park")
		}
		initialSnapshot = waitAfterRestoreSnapshot(t, store, deploymentID, "")
		last = probe(t, bindings[mp.CredentialReadWrite], "restore")
	})
	phase("manual_task_without_migration_secret", func(t *testing.T) {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/tasks", api.CreateAppTaskRequest{Command: []string{"/postgres-probe", "check-runtime"}, TimeoutSeconds: 30})
		var response api.AppTaskResponse
		if status != http.StatusAccepted || json.Unmarshal(body, &response) != nil || response.ID == "" {
			t.Fatalf("manual SQL task admission: status=%d", status)
		}
		postgresNativeWait(t, "manual SQL task", func(ctx context.Context) bool {
			task, err := store.AppTaskByID(ctx, account, appID, response.ID)
			if err != nil {
				return false
			}
			if task.Status.Terminal() && task.Status != state.AppTaskSucceeded {
				t.Fatal("manual SQL task failed")
			}
			return task.Kind == state.AppTaskKindManual && task.Status == state.AppTaskSucceeded
		})
	})
	workload := func(t *testing.T, binding mp.Binding, method string) { last = probe(t, binding, method) }
	rotate := func(t *testing.T, parked bool) {
		t.Helper()
		old := bindings[mp.CredentialReadWrite]
		oldURI := uri(old)
		if !parked {
			// Manual tasks may outlast the serving guest's idle window. Issue
			// real SQL immediately before rotating and require a running guest.
			workload(t, old, "")
		}
		instances, err := store.ListInstancesForApp(t.Context(), appID)
		if err != nil {
			t.Fatal(err)
		}
		running, parkedGuest := false, false
		for _, instance := range instances {
			running = running || state.State(instance.State) == state.StateRunning
			parkedGuest = parkedGuest || state.State(instance.State) == state.StateParked
		}
		if (!parked && !running) || (parked && (!parkedGuest || running)) {
			t.Fatal("SQL rotation did not begin in its required guest state")
		}
		// Inspect the snapshots eligible immediately before this rotation. An
		// earlier rotation or failed restore may already have staled the original
		// capture; checking that original again would not prove invalidation.
		rows, err := pool.Query(t.Context(), `SELECT id FROM snapshots WHERE deployment_id=$1 AND NOT stale`, deploymentID)
		if err != nil {
			t.Fatal(err)
		}
		var eligible []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			eligible = append(eligible, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if parked {
			// Lose the notification before submitting the public intent. Killing
			// schedd afterward could race with a completed retirement and never
			// exercise durable replay at all.
			if err := h.KillSchedd(); err != nil {
				t.Fatal(err)
			}
		}
		body, status := doReq(t, h, key, http.MethodPost, "/v1/postgres/bindings/"+old.ID+"/rotate", nil)
		var response api.ManagedPostgresBinding
		if status != http.StatusOK || json.Unmarshal(body, &response) != nil || response.CredentialGeneration != old.CredentialGeneration+1 || !response.RotationPending {
			t.Fatalf("public SQL rotation failed: status=%d", status)
		}
		rotated, err := fixture.Catalog.GetBinding(t.Context(), account, old.ID)
		if err != nil || rotated.RotationWakeID == "" {
			t.Fatal("public SQL rotation was not committed")
		}
		bindings[mp.CredentialReadWrite] = rotated
		var remaining int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM snapshots WHERE id=ANY($1::uuid[]) AND NOT stale`, eligible).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatal("public rotation left the old SQL credential snapshot eligible")
		}
		if parked {
			// The durable restart must remain replayable across scheduler loss.
			if err := h.RestartSchedd(); err != nil {
				t.Fatal(err)
			}
		}
		postgresNativeWait(t, "scheduler rotation retirement", func(ctx context.Context) bool {
			current, err := fixture.Catalog.GetBinding(ctx, account, old.ID)
			return err == nil && (current.RotationCleanupReady || current.RotationPreviousGeneration == 0)
		})
		for _, oldInstance := range instances {
			if !state.State(oldInstance.State).CountsForRAM() {
				continue
			}
			current, err := store.InstanceByID(t.Context(), oldInstance.ID)
			if err != nil || state.State(current.State).CountsForRAM() {
				t.Fatal("scheduler retired SQL credential before old guest drained")
			}
		}
		postgresNativeWait(t, "public rotation completion", func(ctx context.Context) bool {
			body, status := doReq(t, h, key, http.MethodGet, "/v1/postgres/bindings/"+old.ID, nil)
			var current api.ManagedPostgresBinding
			return status == http.StatusOK && json.Unmarshal(body, &current) == nil && !current.RotationPending && current.CredentialGeneration == rotated.CredentialGeneration
		})
		if fixture.VerifyRevoked(t.Context(), oldURI) != nil {
			t.Fatal("retired credential still authenticates or SQL is unavailable")
		}
		workload(t, rotated, "")
	}
	phase("running_rotation_and_retirement", func(t *testing.T) { rotate(t, false) })
	phase("pooled_disconnect_recovery", func(t *testing.T) {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park pooled SQL app: status=%d", status)
		}
		initialSnapshot = waitAfterRestoreSnapshot(t, store, deploymentID, initialSnapshot)
		// The snapshot retains application pools, but their external TCP
		// sessions are now gone. Only the real restore hook repairs them.
		proxy.Disconnect()
		last = probe(t, bindings[mp.CredentialReadWrite], "restore")
	})
	phase("rotated_snapshot_restore", func(t *testing.T) {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park SQL app: status=%d", status)
		}
		// pooled_disconnect_recovery captured the post-rotation snapshot. Parking
		// the guest restored from it keeps that healthy init capture instead of
		// uploading another one (sched reusableSnapshot), so accept either it or
		// a newer capture, but never a stale one.
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		if _, err := e2etest.WaitForAppParked(ctx, t, pool, appID, 45*time.Second); err != nil {
			t.Fatal("rotated SQL guest did not park")
		}
		snapshot, err := store.LatestSnapshotForTier(ctx, deploymentID, state.SnapshotTierInit)
		if err != nil || snapshot.Stale {
			t.Fatal("rotated SQL guest has no usable init snapshot")
		}
		initialSnapshot = snapshot.ID
		last = probe(t, bindings[mp.CredentialReadWrite], "restore")
	})
	phase("parked_rotation_scheduler_recovery", func(t *testing.T) {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park SQL app: status=%d", status)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 45*time.Second); err != nil {
			t.Fatal(err)
		}
		rotate(t, true)
	})
	expectedNotes := make(map[int64]string)
	for _, starter := range postgresstarter.Starters() {
		postgresNativeStarterPhases(t, starter, expectedNotes, h, key, slug, appID, account, pool, store, fixture, proxy, bindings, phase, rotate, &workload, &deploymentID, &initialSnapshot, recordRuntime)
	}
	phase("independent_cleanup", func(t *testing.T) {
		cleanup()
		after, err := e2etest.PostgresGuestRuntimeManifest(artifactPaths, guestIdentity.Runtime.SourceRevision, true)
		if err != nil || !reflect.DeepEqual(after, guestIdentity.Runtime) {
			t.Fatal("guest qualification artifacts changed during acceptance")
		}
		artifactsVerified = true
	})
}

func postgresNativeWait(t *testing.T, what string, ready func(context.Context) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for !ready(ctx) {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}
