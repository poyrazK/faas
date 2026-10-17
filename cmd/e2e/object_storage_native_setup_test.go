//go:build metal && object_storage_native

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/storagefixture"
	"github.com/onebox-faas/faas/pkg/e2etest/storagestarter"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type storageNativeJourney struct {
	h          *e2etest.Harness
	pool       *pgxpool.Pool
	store      *state.PgStore
	gateway    *storagefixture.Gateway
	provider   *objectStorageS3Stub
	backend    objectstorage.Backend
	identity   *age.X25519Identity
	key        string
	slug       string
	appID      string
	account    string
	token      string
	bucket     api.ObjectBucket
	binding    api.ObjectStorageComputeBinding
	controlID  string
	sourcePath string
	deployment string
	snapshot   string
	expected   map[string][]byte
	starter    storagestarter.Starter
	manifest   storagestarter.RuntimeManifest
	artifacts  map[string]string
	cleaned    bool
}

func storageNativePrerequisites(t *testing.T) {
	t.Helper()
	if os.Getenv("GREGALE_STORAGE_NATIVE_ACCEPTANCE") != "1" {
		t.Fatal("use make test-object-storage-native for isolated storage guest acceptance")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("storage guest acceptance requires root on x86_64 Linux")
	}
	for _, name := range []string{"DATABASE_URL", "FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "FAAS_GUEST_INIT"} {
		if os.Getenv(name) == "" {
			t.Fatalf("missing storage guest prerequisite %s", name)
		}
	}
	if os.Getenv("GREGALE_STORAGE_NATIVE_IP") != "198.18.0.254" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("storage guest acceptance requires its isolated benchmark IP and PostgreSQL")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal("storage guest acceptance requires KVM")
	}
	if err := e2etest.ValidateNativeGuestInit(os.Getenv("FAAS_GUEST_INIT")); err != nil {
		t.Fatal(err)
	}
}

func newStorageNativeJourney(t *testing.T, pool *pgxpool.Pool, starter storagestarter.Starter) *storageNativeJourney {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal("cannot generate storage fixture identity")
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
	provider := newObjectStorageS3Stub(t)
	gateway := storagefixture.NewGateway(t, os.Getenv("GREGALE_STORAGE_NATIVE_IP")+":0")
	store := state.NewPgStore(pool)
	config := storagefixture.ProviderConfig(provider.server.URL, gateway.URL, filepath.Join(keys, "usage.json"))
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "owned-native-fixture" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: store, RequestMetrics: store, SpoolDir: t.TempDir(),
		OpenSecret: func(blob []byte) (string, error) {
			namespace, value, err := secretbox.OpenBytes(identity, blob)
			if err != nil || namespace != s3gateway.CredentialSecretNamespace || len(value) != 40 {
				return "", errors.New("invalid fixture credential")
			}
			return string(value), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetHandler(handler)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(keys, "storage.json")
	if err := os.WriteFile(configPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	storageNativeEnable(t, store)
	environment := []string{"FAAS_OBJECT_STORAGE_CONFIG=" + configPath,
		"FAAS_E2E_S3_ACCESS_KEY=owned-native-fixture", "FAAS_E2E_S3_SECRET_KEY=owned-native-fixture",
		"FAAS_HOST_KEY_PATH=" + filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_IDENTITY_PATH=" + filepath.Join(keys, "host.age"),
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + public, "FAAS_FLEET_AGE_RECIPIENT_PATH=" + public}
	for _, name := range []string{"GRYPE_DB_CACHE_DIR", "GRYPE_DB_AUTO_UPDATE", "FAAS_STORAGE_ROOT"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	registryFixture := e2etest.NewFakeRegistry()
	t.Cleanup(registryFixture.Close)
	base, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "storage-native")
	_ = registryFixture.AddImage("onebox-faas/deploy-base", base)
	e2etest.OverrideDeployBase(t, registryFixture.Host()+"/onebox-faas/deploy-base:latest")
	h := e2etest.Start(t, pool, e2etest.All|e2etest.GatewaydPublic, environment...)
	j := &storageNativeJourney{h: h, pool: pool, store: store, gateway: gateway, provider: provider, backend: backend, identity: identity,
		starter: starter, sourcePath: filepath.Join(t.TempDir(), "source.tar.gz"), expected: make(map[string][]byte), token: strings.Repeat(randHexSuffix(), 8)}
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
		h.Stop()
		// Failure cleanup cannot earn a phase pass. Retire owned custody even if
		// the public API or scheduler failed during the acceptance journey.
		if !j.cleaned && j.binding.ID != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			if _, err := store.RevokeObjectS3ComputeBinding(ctx, j.account, j.bucket.ID, j.binding.ID); err != nil {
				t.Error("failed-journey storage credential cleanup failed")
			}
			if j.controlID != "" {
				if err := store.RevokeObjectS3Credential(ctx, j.account, j.bucket.ID, j.controlID); err != nil && !errors.Is(err, state.ErrNotFound) {
					t.Error("failed-journey control credential cleanup failed")
				}
			}
		}
	})
	j.artifacts, j.manifest = storageNativeArtifacts(t, h, starter.Template)
	j.key = h.SeedAccount(t.Context(), api.PlanPro)
	j.account = accountIDFromKey(t, t.Context(), pool, j.key)
	j.slug = "storage-native-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, j.key, "/v1/apps", api.CreateAppRequest{Slug: j.slug, Type: "app", RAMMB: 256, RequireAuthn: &falsy}); status != http.StatusCreated {
		t.Fatalf("create storage app: status=%d", status)
	}
	j.appID = mustGetAppID(t, h, j.key, j.slug)
	endpoint, _ := url.Parse(gateway.URL)
	port := gatewayPort(t, endpoint)
	ports, destinations := []int{port}, []string{"198.18.0.254/32"}
	if status := statusOnly(t, h, j.key, http.MethodPatch, "/v1/apps/"+j.slug, api.UpdateAppRequest{EgressPorts: &ports, EgressAllowlist: &destinations}); status != http.StatusOK {
		t.Fatalf("declare storage destination through app policy: status=%d", status)
	}
	if status := statusOnly(t, h, j.key, http.MethodPut, "/v1/apps/"+j.slug+"/env/"+starter.TrustEnv, api.PutAppEnvRequest{Value: "/app/" + storagestarter.CAPath}); status != http.StatusOK {
		t.Fatalf("set storage fixture trust: status=%d", status)
	}
	return j
}

func storageNativeEnable(t *testing.T, store *state.PgStore) {
	t.Helper()
	row, err := store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: "s3_enabled", Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage("true"), ApplyMode: state.RuntimeConfigApplyHot, Reason: "isolated storage acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRuntimeConfigApplied(t.Context(), row.Key, row.Scope, row.ScopeID, row.Version, json.RawMessage("true"), ""); err != nil {
		t.Fatal(err)
	}
}

func storageNativeArtifacts(t *testing.T, h *e2etest.Harness, template string) (map[string]string, storagestarter.RuntimeManifest) {
	t.Helper()
	revision, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal("storage acceptance requires a checkout revision")
	}
	paths := map[string]string{"kernel": os.Getenv("FAAS_TEST_KERNEL"), "guest_init": os.Getenv("FAAS_GUEST_INIT"), "builder_base": e2etest.StagedBuilderBasePath()}
	for _, name := range append(append([]string(nil), e2etest.DaemonBinaries...), e2etest.StaticHelperBinaries...) {
		paths[name] = filepath.Join(h.BinDir, name)
	}
	for _, name := range []string{"firecracker", "jailer"} {
		paths[name], err = exec.LookPath(name)
		if err != nil {
			t.Fatal("storage acceptance host executable missing")
		}
	}
	paths["e2e_test"], err = os.Executable()
	if err != nil {
		t.Fatal("storage acceptance test executable missing")
	}
	manifest, err := e2etest.StorageGuestRuntimeManifest(paths, strings.TrimSpace(string(revision)), template, false)
	if err != nil {
		t.Fatal(err)
	}
	return paths, manifest
}
