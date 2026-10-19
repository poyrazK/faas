package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *testHost) HostingRetentionEnabled() bool                      { return h.retention }
func (h *testHost) HostingRetentionStore() state.HostingRetentionStore { return h.store }

func TestHostingRetentionHandlersAreOptInBoundedAndRevisionChecked(t *testing.T) {
	// adr: 202610171001 — reading a plan never enables cleanup; stale policy writes fail.
	h, s, account, site := retentionFixture(t)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		if method == "GET" {
			s.GetCleanupPlan(w, r, account)
		} else {
			s.SetRetentionPolicy(w, r, account)
		}
		return w
	}
	if w := request("GET", "/", ""); w.Code != 503 {
		t.Fatal("default-off", w.Code, w.Body)
	}
	h.retention = true
	for _, path := range []string{"/?pending_age_seconds=3600", "/?pending_age_seconds=1&staged_age_seconds=3600", "/?pending_age_seconds=3600&staged_age_seconds=3600&pending_age_seconds=3600"} {
		if w := request("GET", path, ""); w.Code != 400 {
			t.Fatal(path, w.Code, w.Body)
		}
	}
	w := request("GET", "/?pending_age_seconds=3600&staged_age_seconds=7200", "")
	var plan api.HostingCleanupPlan
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &plan) != nil || plan.Policy.Enabled || plan.Policy.Revision != 0 || !plan.Proposed || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	for _, body := range []string{`{"pending_age_seconds":3600,"staged_age_seconds":3600,"expected_revision":0}`, `{"enabled":true,"pending_age_seconds":3600,"staged_age_seconds":3600}`} {
		if w := request("PUT", "/", body); w.Code != 400 {
			t.Fatal("implicit intent accepted", w.Code, w.Body)
		}
	}
	body := `{"enabled":true,"pending_age_seconds":3600,"staged_age_seconds":3600,"expected_revision":0}`
	if w = request("PUT", "/", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w = request("PUT", "/", body); w.Code != 409 {
		t.Fatal("stale update", w.Code, w.Body)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("site", uuid.NewString())
	w = httptest.NewRecorder()
	s.GetCleanupPlan(w, r, account)
	if w.Code != 404 {
		t.Fatal("missing ownership", w.Code, w.Body)
	}
	h.retention = false
	if w = request("PUT", "/", body); w.Code != 503 {
		t.Fatal("hot disable", w.Code, w.Body)
	}
}

func TestHostingRetentionWorkerNeedsBothOptInsAndAuthorization(t *testing.T) {
	// adr: 202610171001 — worker authority is separate from CI tokens and existing GC.
	h, _, account, site := retentionFixture(t)
	ctx := t.Context()
	r, err := h.store.CreateHostingRelease(ctx, account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.SetHostingRetentionPolicy(ctx, account.ID, site.ID, api.SetHostingRetentionRequest{Enabled: true, HostingRetentionRules: api.HostingRetentionRules{PendingAgeSeconds: 3600, StagedAgeSeconds: 3600}}); err != nil {
		t.Fatal(err)
	}
	enabled, allowed := false, false
	worker := RetentionReconciler{Store: h.store, OriginID: site.OriginID, Enabled: func() bool { return enabled }, Authorize: func(_ context.Context, _ string) (bool, error) { return allowed, nil }, Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	for _, values := range [][2]bool{{false, true}, {true, false}, {true, true}} {
		enabled, allowed = values[0], values[1]
		if err = worker.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		release, err := h.store.GetHostingRelease(ctx, account.ID, site.ID, r.ID)
		if err != nil || (release.State == "retiring") != (enabled && allowed) {
			t.Fatal(release, err)
		}
	}
}

func retentionFixture(t *testing.T) (*testHost, Service, state.Account, api.HostingSite) {
	t.Helper()
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	h := &testHost{enabled: true, allowed: true, durable: true, store: store, artifacts: &testArtifacts{}}
	return h, Service{Host: h}, account, site
}

func TestHostingRetentionUsesPublicationGraceAndKeepsReservationOnCleanupFailure(t *testing.T) {
	// adr: 202610171001 — automatic intent uses existing acknowledged retirement and retrying GC.
	synctest.Test(t, func(t *testing.T) {
		h, _, account, site := retentionFixture(t)
		ctx := t.Context()
		request := api.HostingCIRunRequest{Manifest: api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}, PreviewTTLSeconds: 3600}
		run, err := h.store.CreateHostingCIRun(ctx, account.ID, site.ID, uuid.NewString(), request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.store.ReadyHostingRelease(ctx, account.ID, site.ID, run.ReleaseID); err != nil {
			t.Fatal(err)
		}
		if _, err = h.store.SetHostingRetentionPolicy(ctx, account.ID, site.ID, api.SetHostingRetentionRequest{Enabled: true, HostingRetentionRules: api.HostingRetentionRules{PendingAgeSeconds: 3600, StagedAgeSeconds: 3600}}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Hour)
		if ids, err := h.store.ApplyHostingRetention(ctx, account.ID, site.ID, site.OriginID, time.Now()); err != nil || len(ids) != 1 {
			t.Fatal(ids, err)
		}
		before, err := h.store.GetHostingUsage(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		sink := &routeSink{}
		artifacts := &routeArtifacts{removeFailure: true}
		gc := Reconciler{Store: h.store, Artifacts: artifacts, LeasedRoutes: sink, DurableRoutes: sink, AdmissionEnabled: func() bool { return false }}
		if err = gc.RunOnce(ctx); err != nil {
			t.Fatal("removed before publication grace", err)
		}
		time.Sleep(api.HostingRetirementGrace + time.Second)
		if err = gc.RunOnce(ctx); err == nil {
			t.Fatal("provider removal failure ignored")
		}
		after, err := h.store.GetHostingUsage(ctx, account.ID)
		if err != nil || after.ReservedBytes != before.ReservedBytes {
			t.Fatal("reservation released on failed cleanup", after, err)
		}
		artifacts.removeFailure = false
		if err = gc.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		after, err = h.store.GetHostingUsage(ctx, account.ID)
		if err != nil || after.ReservedBytes != 0 {
			t.Fatal("cleanup did not release reservation", after, err)
		}
		retry, err := h.store.CreateHostingCIRun(ctx, account.ID, site.ID, run.ID, request)
		if err != nil || retry.ReleaseID != run.ReleaseID || retry.PreviewID != run.PreviewID {
			t.Fatal("GC lost run identity", retry, err)
		}
	})
}

type retentionPageStore struct {
	state.HostingRetentionStore
	seen []string
}

func (s *retentionPageStore) ListHostingRetentionSites(_ context.Context, after string, limit int) ([]api.HostingSite, error) {
	if limit != api.HostingRetentionSitesPerPass {
		return nil, errors.New("unbounded scan")
	}
	out := []api.HostingSite{}
	for i := 0; i < api.HostingRetentionSitesPerPass+1; i++ {
		id := fmt.Sprintf("%04d", i)
		if id > after && len(out) < limit {
			out = append(out, api.HostingSite{ID: id, AccountID: "account", OriginID: "origin"})
		}
	}
	return out, nil
}
func (s *retentionPageStore) ApplyHostingRetention(_ context.Context, _, site, _ string, _ time.Time) ([]string, error) {
	s.seen = append(s.seen, site)
	if site == "0000" {
		return nil, errors.New("one site failed")
	}
	return nil, nil
}
func TestHostingRetentionPassIsBoundedAndAdvancesPastFailedSites(t *testing.T) {
	// adr: 202610171001 — one failure cannot starve later sites or grow a pass beyond its bound.
	store := &retentionPageStore{}
	worker := RetentionReconciler{Store: store, OriginID: "origin", Enabled: func() bool { return true }, Authorize: func(context.Context, string) (bool, error) { return true, nil }}
	if err := worker.RunOnce(t.Context()); err == nil || len(store.seen) != api.HostingRetentionSitesPerPass {
		t.Fatal("unbounded/hidden failure", len(store.seen), err)
	}
	if err := worker.RunOnce(t.Context()); err != nil || len(store.seen) != api.HostingRetentionSitesPerPass+1 {
		t.Fatal("cursor starved tail", len(store.seen), err)
	}
}

func TestHostingUploadAndVerificationRenewActivityEvenWithRetentionFlagOff(t *testing.T) {
	// adr: 202610171001 — hot enabling retention cannot retire a job already admitted by a new writer.
	synctest.Test(t, func(t *testing.T) {
		h, service, account, site := retentionFixture(t)
		ctx := t.Context()
		release, err := h.store.CreateHostingRelease(ctx, account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
		if err != nil {
			t.Fatal(err)
		}
		before := release.LastActivityAt
		time.Sleep(2 * time.Hour)
		request := func() *http.Request {
			r := httptest.NewRequest("POST", "/", nil)
			r.SetPathValue("site", site.ID)
			r.SetPathValue("release", release.ID)
			return r
		}
		w := httptest.NewRecorder()
		service.Uploads(w, request(), account)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		release, err = h.store.GetHostingRelease(ctx, account.ID, site.ID, release.ID)
		if err != nil || !release.LastActivityAt.After(before) {
			t.Fatal("upload activity missing", release, err)
		}
		before = release.LastActivityAt
		time.Sleep(2 * time.Hour)
		w = httptest.NewRecorder()
		service.Finalize(w, request(), account)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
		release, err = h.store.GetHostingRelease(ctx, account.ID, site.ID, release.ID)
		if err != nil || !release.LastActivityAt.After(before) || h.retention {
			t.Fatal("verification activity missing", release, err)
		}
	})
}
