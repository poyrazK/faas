//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresfixture"
	"github.com/onebox-faas/faas/pkg/e2etest/postgresprobe"
	mp "github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestManagedPostgresNativeMetal exercises real SQL in deployment release,
// serving, task, replacement and restored guests. Only provider management is
// simulated: the catalog, sealed credentials, daemons and SQL authority are real.
func TestManagedPostgresNativeMetal(t *testing.T) {
	if os.Getenv("GREGALE_POSTGRES_NATIVE_ACCEPTANCE") != "1" {
		t.Skip("use make test-managed-postgres-native for isolated SQL guest acceptance")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("SQL guest acceptance requires root on x86_64 Linux")
	}
	for _, required := range []string{"DATABASE_URL", "FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "GREGALE_POSTGRES_NATIVE_SQL_IP"} {
		if os.Getenv(required) == "" {
			t.Fatalf("missing native SQL prerequisite %s", required)
		}
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("SQL guest acceptance cannot skip PostgreSQL")
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
	h := e2etest.Start(t, pool, e2etest.All,
		"FAAS_RELEASE_PHASE_ENABLED=1",
		"FAAS_APP_TASK_DISPATCH=1",
		"FAAS_HOST_KEY_PATH="+filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_IDENTITY_PATH="+filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_RECIPIENT_PATH="+public,
		"FAAS_FLEET_AGE_RECIPIENT_PATH="+public,
	)
	key := h.SeedAccount(t.Context(), api.PlanPro)
	store := state.NewPgStore(pool)
	account := accountIDFromKey(t, t.Context(), pool, key)
	slug := "postgres-native-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RAMMB: 256, RequireAuthn: &falsy}); status != http.StatusCreated {
		t.Fatalf("create SQL app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	ports := []int{5432}
	if status := statusOnly(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{EgressPorts: &ports}); status != http.StatusOK {
		t.Fatalf("declare SQL TCP port through app policy: status=%d", status)
	}
	hmac, err := os.ReadFile(h.HostHMACKeyPath)
	if err != nil {
		t.Fatal("read isolated host signing key")
	}
	fixture := postgresfixture.New(t, pool, identity, hmac, proxy.Host, proxy.Port)
	if fixture == nil {
		t.Fatal("customer SQL fixture missing")
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
	phase("release_and_initial_restore", func(t *testing.T) {
		binary, err := e2etest.PostgresProbeExecutable()
		if err != nil {
			t.Fatal(err)
		}
		source := buildTarGz(t, map[string]string{
			"Dockerfile":       "FROM scratch\nCOPY --chmod=0755 postgres-probe /postgres-probe\nCOPY --chmod=0755 postgres-probe /bin/sh\nEXPOSE 8080\nCMD [\"/postgres-probe\"]\n",
			"postgres-probe":   string(binary),
			"gregale.yaml":     "release:\n  command: /postgres-probe migrate\n",
			"faas-build-token": runID,
		})
		body, status := postMultipartDeployment(t, h, key, slug, source, true, "")
		if status != http.StatusAccepted {
			t.Fatalf("deploy SQL source: status=%d", status)
		}
		deploymentID, _ = parseQueuedDeployment(t, body)
		ctx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
		defer cancel()
		if _, _, err := e2etest.WaitForSourceDeployment(ctx, t, pool, deploymentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
			t.Fatalf("SQL source deployment: %v", err)
		}
		release, err := store.ReleaseAppTaskByDeployment(ctx, deploymentID)
		if err != nil || release.Kind != state.AppTaskKindRelease || release.Status != state.AppTaskSucceeded {
			t.Fatal("normal deployment did not complete its SQL migration release")
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
	rotate := func(t *testing.T, parked bool) {
		t.Helper()
		old := bindings[mp.CredentialReadWrite]
		oldURI := uri(old)
		if !parked {
			// Manual tasks may outlast the serving guest's idle window. Issue
			// real SQL immediately before rotating and require a running guest.
			last = probe(t, old, "")
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
		rotated, err := fixture.Bindings.Rotate(t.Context(), account, old.ID)
		if err != nil {
			t.Fatal("SQL credential rotation failed")
		}
		bindings[mp.CredentialReadWrite] = rotated
		// Exercise the production rotation's durable restart seam. Provider
		// management runs in-process; no retirement receipt is fabricated here.
		if _, err := state.InvalidateAppSnapshotsAtExistingStamp(t.Context(), store, appID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CompareAndSetAppStatus(t.Context(), appID, state.AppActive, state.AppEvictedCold); err != nil {
			t.Fatal("rotation runtime refresh claim failed")
		}
		var stale bool
		if err := pool.QueryRow(t.Context(), `SELECT stale FROM snapshots WHERE id=$1`, initialSnapshot).Scan(&stale); err != nil || !stale {
			t.Fatal("rotation left the old SQL credential snapshot eligible")
		}
		payload, err := json.Marshal(map[string]string{"app_id": appID, "wake_id": rotated.RotationWakeID})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Notify(t.Context(), pool, db.NotifyRuntimeConfigRestart, string(payload)); err != nil {
			t.Fatal(err)
		}
		if parked {
			// The durable restart must remain replayable across scheduler loss.
			if err := h.RestartSchedd(); err != nil {
				t.Fatal(err)
			}
		}
		postgresNativeWait(t, "scheduler rotation retirement", func(ctx context.Context) bool {
			current, err := fixture.Catalog.GetBinding(ctx, account, old.ID)
			return err == nil && current.RotationCleanupReady
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
		if _, err := fixture.Bindings.ReconcileRotationCleanup(t.Context(), account, old.ID); err != nil {
			t.Fatal("credential retirement reconciliation failed")
		}
		if fixture.VerifyRevoked(t.Context(), oldURI) != nil {
			t.Fatal("retired credential still authenticates or SQL is unavailable")
		}
		last = probe(t, rotated, "")
	}
	phase("running_rotation_and_retirement", func(t *testing.T) { rotate(t, false) })
	phase("rotated_snapshot_restore", func(t *testing.T) {
		if _, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusNoContent {
			t.Fatalf("park SQL app: status=%d", status)
		}
		initialSnapshot = waitAfterRestoreSnapshot(t, store, deploymentID, initialSnapshot)
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
	phase("independent_cleanup", func(t *testing.T) { cleanup() })
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
