package object

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type storageControlHost struct {
	Host
	store    state.Store
	registry *objectstorage.Registry
	enabled  bool
}

func (h *storageControlHost) Store() state.Store                                         { return h.store }
func (h *storageControlHost) ObjectStorage() *objectstorage.Registry                     { return h.registry }
func (h *storageControlHost) S3RuntimeEnabled() bool                                     { return h.enabled }
func (h *storageControlHost) EmitAudit(context.Context, string, *string, map[string]any) {}
func (h *storageControlHost) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
func (h *storageControlHost) LoadApp(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool) {
	return (&authmw.Middleware{Authn: h.store}).LoadApp(w, r, acct, slug)
}

func storageControlStores(t *testing.T, run func(*testing.T, state.Store)) {
	t.Helper()
	t.Run("memory", func(t *testing.T) { run(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		run(t, state.NewPgStore(pool))
	})
}

func newStorageControlFixture(t *testing.T, st state.Store, endpoint string) (*Service, *storageControlHost, state.Account, state.App, state.ObjectBucket) {
	t.Helper()
	since := time.Now().UTC().Add(-time.Hour)
	policy := api.ObjectStoragePolicy{AccountingMode: api.ObjectStorageGatewaySafetyV1, GatewayMeteringSince: &since,
		MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyRequests: 1,
		MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, PublicEndpoint: "https://s3.test",
		Transfer: objectstorage.ObjectTransferConfig{Profile: "proxied"}, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "native"},
		Backends: []objectstorage.BackendConfig{{ID: "native", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "control-test",
			Endpoint: endpoint, AllowHTTP: true, PathStyle: true, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET",
			Encryption: objectstorage.EncryptionConfig{Algorithms: []string{"AES256"}}}}}, func(string) string { return "fixture" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	acct, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := st.CreateProject(t.Context(), state.Project{AccountID: acct.ID, Slug: "allowed"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "allowed-api-" + uuid.NewString()[:8], WorkloadName: "api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	host := &storageControlHost{store: st, registry: registry, enabled: true}
	b := newStorageControlBucket(t, st, registry, acct, app, "destination")
	accounting := st.(state.ObjectStorageAccountingStore)
	inventoryToken := uuid.NewString()
	if err = accounting.ClaimObjectInventory(t.Context(), b.ID, inventoryToken); err != nil {
		t.Fatal(err)
	}
	if err = accounting.FinishObjectInventory(t.Context(), b.ID, inventoryToken, 0, 0); err != nil {
		t.Fatal(err)
	}
	return &Service{Host: host}, host, acct, app, b
}

func newStorageControlBucket(t *testing.T, st state.Store, registry *objectstorage.Registry, acct state.Account, app state.App, name string) state.ObjectBucket {
	t.Helper()
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	buckets := st.(state.ObjectBucketStore)
	b, err := buckets.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: name,
		Scope: "default", PhysicalName: "physical-" + uuid.NewString(), BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: backend.Region}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(t.Context(), acct.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(t.Context(), b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	b, err = buckets.GetObjectBucket(t.Context(), acct.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
