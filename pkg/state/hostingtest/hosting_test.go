package hostingtest

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type hostingTestStore interface {
	state.HostingCancellationStore
	state.HostingPreviewCapacityReader
	state.HostingActivityReader
	state.HostingOIDCStore
	state.Store
	state.HostingStore
	state.HostingDomainStore
	state.HostingRetentionStore
	state.HostingAutomationStore
	state.HostingHistoryStore
	state.HostingPreviewStore
}

func TestHostingStores(t *testing.T) {
	// adr: 20261015 — immutable intent remains account-owned and promotion is CAS.
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var store hostingTestStore = state.NewMemStore()
			if backend == "postgres" {
				pool := pgtest.OpenMigrated(t)
				if err := db.MigrateUp(t.Context(), pool); err != nil {
					t.Fatal(err)
				}
				store = state.NewPgStore(pool)
			}
			t.Run("ownership_and_promotion", func(t *testing.T) { lifecycle(t, store) })
			t.Run("concurrent_quota", func(t *testing.T) { quota(t, store) })
			t.Run("parent_cleanup", func(t *testing.T) { parentCleanup(t, store) })
			t.Run("durable_routes", func(t *testing.T) { durableLifecycle(t, store) })
			t.Run("durable_cleanup", func(t *testing.T) { durableEmptySiteCleanup(t, store) })
			t.Run("durable_release_cleanup", func(t *testing.T) { durableReleaseCleanup(t, store) })
			t.Run("durable_snapshot_concurrency", func(t *testing.T) { durableSnapshotConcurrency(t, store) })
			t.Run("publication_status", func(t *testing.T) { publicationStatusLifecycle(t, store) })
			t.Run("pending_upload_usage", func(t *testing.T) { pendingUploadUsage(t, store) })
			t.Run("account_storage_usage", func(t *testing.T) { accountStorageUsage(t, store) })
			t.Run("staged_release_visibility", func(t *testing.T) { stagedReleaseVisibility(t, store) })
			t.Run("oidc_credentials", func(t *testing.T) { hostingOIDCLifecycle(t, store) })
			t.Run("deployment_activity", func(t *testing.T) { hostingActivityLifecycle(t, store) })
			t.Run("automation_credentials", func(t *testing.T) { hostingCredentialLifecycle(t, store) })
			t.Run("retention", func(t *testing.T) { hostingRetentionLifecycle(t, store) })
			t.Run("retention_races", func(t *testing.T) { hostingRetentionRaces(t, store) })
			t.Run("ci_cancellation", func(t *testing.T) { hostingCancellation(t, store) })
			t.Run("ci_cancellation_protection", func(t *testing.T) { hostingCancellationProtection(t, store) })
			t.Run("ci_cancellation_races", func(t *testing.T) { hostingCancellationRaces(t, store) })
			t.Run("preview_reservations", func(t *testing.T) { hostingPreviewReservations(t, store) })
			t.Run("ci_run_identity", func(t *testing.T) { hostingCIRunLifecycle(t, store) })
			t.Run("release_history", func(t *testing.T) { hostingHistoryLifecycle(t, store) })
			t.Run("history_retention", func(t *testing.T) { hostingHistoryRetention(t, store) })
			t.Run("private_previews", func(t *testing.T) { previewLifecycle(t, store) })
			t.Run("preview_quota_expiry", func(t *testing.T) { previewExpiryAndQuota(t, store) })
			t.Run("custom_domains", func(t *testing.T) { customDomainLifecycle(t, store) })
			t.Run("custom_domain_recovery", func(t *testing.T) { customDomainRecovery(t, store) })
			t.Run("custom_domain_quota", func(t *testing.T) { customDomainQuota(t, store) })
		})
	}
}

func parentCleanup(t *testing.T, store hostingTestStore) {
	account, site := seed(t, store)
	if err := store.DeleteProject(t.Context(), site.ProjectID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("project deletion lost hosting catalog: %v", err)
	}
	if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: account.ID, ProjectID: site.ProjectID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	staging, err := store.CreateHostingSite(t.Context(), api.HostingSite{ID: id, AccountID: account.ID, ProjectID: site.ProjectID, Environment: "stage", Name: "staging", Hostname: id + ".hosting.example", OriginID: site.OriginID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(t.Context(), account.ID, site.ProjectID, "stage"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("environment deletion lost hosting catalog: %v", err)
	}
	if _, err = store.DeleteHostingSite(t.Context(), account.ID, staging.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingSite(t.Context(), account.ID, staging.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteProjectEnvironment(t.Context(), account.ID, site.ProjectID, "stage"); err != nil {
		t.Fatalf("environment remained blocked after cleanup: %v", err)
	}
}

func seed(t *testing.T, store hostingTestStore) (state.Account, api.HostingSite) {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@hosting.example", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(t.Context(), state.Project{AccountID: account.ID, Slug: "hosting"})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	site, err := store.CreateHostingSite(t.Context(), api.HostingSite{ID: id, AccountID: account.ID, ProjectID: project.ID, Name: "web", Environment: "production", Hostname: id + ".hosting.example", OriginID: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	return account, site
}

func manifest() api.HostingManifest {
	digest := sha256.Sum256([]byte("<html>hello</html>"))
	return api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", SHA256: hex.EncodeToString(digest[:]), Size: 18, ContentType: "text/html"}}}
}

func lifecycle(t *testing.T, store hostingTestStore) {
	account, site := seed(t, store)
	other, _ := seed(t, store)
	if _, err := store.GetHostingSite(t.Context(), other.ID, site.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account site: %v", err)
	}
	if _, err := store.CreateHostingRelease(t.Context(), other.ID, site.ID, manifest()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account release: %v", err)
	}
	input := manifest()
	input.Config = &api.HostingReleaseConfig{Version: 1, Redirects: []api.HostingRedirect{{From: "/old", To: "/new", Status: 308}}, Headers: []api.HostingHeaderRule{{Path: "/*", Values: map[string]string{"x-frame-options": "DENY"}}}}
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Files[0].Path = "mutated.html"
	input.Config.Redirects[0].To = "/mutated"
	input.Config.Headers[0].Values["x-frame-options"] = "SAMEORIGIN"
	release.Manifest.Config.Headers[0].Values["x-frame-options"] = "OTHER"
	loaded, err := store.GetHostingRelease(t.Context(), account.ID, site.ID, release.ID)
	if err != nil || loaded.Manifest.Files[0].Path != "index.html" || loaded.Manifest.Config.Redirects[0].To != "/new" || loaded.Manifest.Config.Headers[0].Values["x-frame-options"] != "DENY" {
		t.Fatalf("mutable manifest: %+v %v", loaded, err)
	}
	// adr: 202610161909 — release response policies are immutable intent.
	loaded.Manifest.Config.Headers[0].Values["x-frame-options"] = "MUTATED"
	reread, err := store.GetHostingRelease(t.Context(), account.ID, site.ID, release.ID)
	if err != nil || reread.Manifest.Config.Headers[0].Values["x-frame-options"] != "DENY" {
		t.Fatal("read mutated stored release configuration")
	}
	if _, err := store.PromoteHostingRelease(t.Context(), account.ID, site.ID, release.ID, site.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("promoted pending release: %v", err)
	}
	if _, err := store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := store.PromoteHostingRelease(t.Context(), account.ID, site.ID, release.ID, site.Revision)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, state.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("CAS results success=%d conflict=%d", success, conflict)
	}
	if _, err := store.RetireHostingRelease(t.Context(), account.ID, site.ID, release.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retired active release: %v", err)
	}
	second, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, second.ID, 2); err != nil {
		t.Fatal(err)
	}
	rolledBack, err := store.PromoteHostingRelease(t.Context(), account.ID, site.ID, release.ID, 3)
	if err != nil || rolledBack.ActiveReleaseID != release.ID || rolledBack.Revision != 4 {
		t.Fatalf("rollback: %+v %v", rolledBack, err)
	}
	retired, err := store.RetireHostingRelease(t.Context(), account.ID, site.ID, second.ID)
	if err != nil || retired.RetiredAt == nil {
		t.Fatalf("retire: %+v %v", retired, err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, second.ID); err == nil {
		t.Fatal("resurrected retired release")
	}
	deleting, err := store.DeleteHostingSite(t.Context(), account.ID, site.ID)
	if err != nil || deleting.Status != "deleting" || deleting.ActiveReleaseID != "" {
		t.Fatalf("delete site: %+v %v", deleting, err)
	}
	if _, err = store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest()); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deleting site admitted release: %v", err)
	}
	if _, err = store.PromoteHostingRelease(t.Context(), account.ID, site.ID, release.ID, deleting.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("deleting site admitted promotion: %v", err)
	}
	remaining, err := store.ListHostingReleases(t.Context(), account.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range remaining {
		if r.State != "retiring" {
			t.Fatalf("site deletion retained live release: %+v", r)
		}
		if err := store.DeleteHostingReleaseRecord(t.Context(), account.ID, site.ID, r.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.PurgeHostingSite(t.Context(), account.ID, site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.GetHostingSite(t.Context(), account.ID, site.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("site not removed: %v", err)
	}
}

func quota(t *testing.T, store hostingTestStore) {
	account, site := seed(t, store)
	results := make(chan error, api.HostingReleasesPerSite+5)
	var wg sync.WaitGroup
	for range api.HostingReleasesPerSite + 5 {
		wg.Go(func() {
			_, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, manifest())
			results <- err
		})
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else if !errors.Is(err, state.ErrHostingQuota) {
			t.Fatal(err)
		}
	}
	if int64(accepted) != api.HostingAccountPendingUploadsMax {
		t.Fatalf("admitted %d releases", accepted)
	}
}
