package hostingtest

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func hostingCredentialLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610170925 — site-bound credentials are hashed, bounded, expiring and revocable.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	plain, hash, err := api.GenerateHostingAutomationToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	input := api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, Label: "CI", Operations: []string{"stage", "preview"}, Hash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	c, err := store.CreateHostingAutomationCredential(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.Operations[0] = "admin"
	c.Operations[0] = "admin"
	owned, key, err := store.AuthenticateHostingAutomationToken(ctx, api.HashAPIKey(plain))
	if err != nil || owned.ID != account.ID || key.HostingSiteID != site.ID || key.HostingOperations[0] != "stage" {
		t.Fatal(owned, key, err)
	}
	rows, err := store.ListHostingAutomationCredentials(ctx, account.ID, site.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	data, _ := json.Marshal(rows)
	if strings.Contains(string(data), plain) || strings.Contains(string(data), "token_hash") || len(rows[0].Hash) != 0 {
		t.Fatal("credential material exposed")
	}
	if err = store.RevokeHostingAutomationCredential(ctx, uuid.NewString(), site.ID, c.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign revoke", err)
	}
	if err = store.RevokeHostingAutomationCredential(ctx, account.ID, site.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AuthenticateHostingAutomationToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revocation cached", err)
	}
	expired := input
	expired.ID = uuid.NewString()
	expired.Operations = []string{"stage"}
	expired.CreatedAt = now.Add(-2 * time.Hour)
	expired.ExpiresAt = now.Add(-time.Hour)
	if _, err = store.CreateHostingAutomationCredential(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AuthenticateHostingAutomationToken(ctx, hash); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("expired token authenticated", err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int64
	for range api.HostingAutomationCredentialsPerSite + 4 {
		wg.Go(func() {
			_, hash, err := api.GenerateHostingAutomationToken()
			if err != nil {
				t.Error(err)
				return
			}
			c := api.HostingAutomationCredential{ID: uuid.NewString(), AccountID: account.ID, SiteID: site.ID, Label: "parallel", Operations: []string{"stage"}, Hash: hash, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
			_, err = store.CreateHostingAutomationCredential(ctx, c)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, state.ErrHostingQuota) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != api.HostingAutomationCredentialsPerSite {
		t.Fatal("credential quota raced", accepted.Load())
	}
}
func hostingCIRunLifecycle(t *testing.T, store hostingTestStore) {
	// adr: 202610170925 — stable intent survives retries, conflict, revocation and catalog GC.
	account, site := seedDurable(t, store)
	ctx := t.Context()
	id := uuid.NewString()
	req := api.HostingCIRunRequest{Manifest: manifest(), Provenance: &api.HostingReleaseProvenance{Description: "CI run"}, PreviewTTLSeconds: 3600}
	var wg sync.WaitGroup
	out := make(chan api.HostingCIRun, 12)
	for range 12 {
		wg.Go(func() {
			run, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, id, req)
			if err != nil {
				t.Error(err)
				return
			}
			out <- run
		})
	}
	wg.Wait()
	close(out)
	var run api.HostingCIRun
	for r := range out {
		if run.ID != "" && (run.ReleaseID != r.ReleaseID || run.PreviewID != r.PreviewID) {
			t.Fatal("duplicate identity")
		}
		run = r
	}
	releases, err := store.ListHostingReleases(ctx, account.ID, site.ID)
	if err != nil || len(releases) != 1 || releases[0].Provenance.Description != "CI run" || releases[0].FirstPromotedAt != nil {
		t.Fatal(releases, err)
	}
	different := req
	different.PreviewTTLSeconds++
	if _, err = store.CreateHostingCIRun(ctx, account.ID, site.ID, id, different); !errors.Is(err, state.ErrConflict) {
		t.Fatal("mutated intent accepted", err)
	}
	if _, err = store.GetHostingCIRun(ctx, uuid.NewString(), site.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign run", err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("unverified preview", err)
	}
	if _, err = store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		wg.Go(func() {
			p, err := store.CreateHostingCIPreview(ctx, account.ID, site.ID, id)
			if err != nil || p.ID != run.PreviewID || p.ReleaseID != run.ReleaseID {
				t.Error(p, err)
			}
		})
	}
	wg.Wait()
	previews, err := store.ListHostingPreviews(ctx, account.ID, site.ID)
	if err != nil || len(previews) != 1 {
		t.Fatal(previews, err)
	}
	p, err := store.RevokeHostingPreview(ctx, account.ID, site.ID, run.PreviewID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.FinishHostingPreview(ctx, p, time.Now().Add(-2*api.HostingPreviewRemovalGrace), ""); err != nil {
		t.Fatal(err)
	}
	if err = store.PurgeHostingPreview(ctx, p, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateHostingCIPreview(ctx, account.ID, site.ID, id); !errors.Is(err, state.ErrConflict) {
		t.Fatal("purged preview recreated", err)
	}
	if _, err = store.RetireHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	snap, err := store.HostingRouteSnapshot(ctx, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingRoute(ctx, site.ID, snap.Generation, time.Now().Add(-2*api.HostingRetirementGrace)); err != nil {
		t.Fatal(err)
	}
	if err = store.DeleteHostingReleaseRecord(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
		t.Fatal(err)
	}
	again, err := store.CreateHostingCIRun(ctx, account.ID, site.ID, id, req)
	if err != nil || again.ReleaseID != run.ReleaseID || again.PreviewID != run.PreviewID {
		t.Fatal("run changed after GC", again, err)
	}
	releases, err = store.ListHostingReleases(ctx, account.ID, site.ID)
	if err != nil || len(releases) != 0 {
		t.Fatal("late retry allocated files", releases, err)
	}
}
