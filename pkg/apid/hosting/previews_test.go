package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	artifacts "github.com/onebox-faas/faas/pkg/hosting"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPreviewHandlersGatesOwnershipExpiryAndRevocation(t *testing.T) {
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	r, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, r.ID); err != nil {
		t.Fatal(err)
	}
	h := &testHost{enabled: true, allowed: true, durable: true, store: store, artifacts: &testArtifacts{}}
	s := Service{Host: h}
	request := func(body string) *http.Request {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.SetPathValue("site", site.ID)
		return req
	}
	w := httptest.NewRecorder()
	s.CreatePreview(w, request(`{"release_id":"`+r.ID+`"}`), account)
	if w.Code != 503 {
		t.Fatal("default off", w.Code)
	}
	called := false
	s.RequirePreviews(func(http.ResponseWriter, *http.Request, state.Account) { called = true })(httptest.NewRecorder(), request("{}"), account)
	if called {
		t.Fatal("disabled replay reached mutation")
	}
	h.previews = true
	for _, ttl := range []string{"-1", "299", "259201", "9223372036854775807"} {
		w = httptest.NewRecorder()
		s.CreatePreview(w, request(`{"release_id":"`+r.ID+`","ttl_seconds":`+ttl+`}`), account)
		if w.Code != 400 {
			t.Fatal("invalid TTL", ttl, w.Code)
		}
	}
	w = httptest.NewRecorder()
	s.CreatePreview(w, request(`{"release_id":"`+r.ID+`","ttl_seconds":300}`), account)
	if w.Code != 202 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Code, w.Body)
	}
	var p api.HostingPreview
	if err = json.Unmarshal(w.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.ReleaseID != r.ID || !strings.HasPrefix(p.Hostname, "p-"+p.ID+".") || p.ExpiresAt.Sub(p.CreatedAt) != api.HostingPreviewTTLMin {
		t.Fatal(p)
	}
	req := request(`{"expected_generation":1}`)
	req.SetPathValue("preview", p.ID)
	w = httptest.NewRecorder()
	s.RevokePreview(w, req, state.Account{ID: uuid.NewString()})
	if w.Code != 404 {
		t.Fatal("foreign mutation", w.Code)
	}
	w = httptest.NewRecorder()
	s.RevokePreview(w, req, account)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	s.RevokePreview(w, request(`{"expected_generation":2}`), account)
	if w.Code != 400 {
		t.Fatal("unreviewed revoke", w.Code)
	}
	h.previews = false
	w = httptest.NewRecorder()
	s.ListPreviews(w, request(""), account)
	if w.Code != 503 {
		t.Fatal("read bypassed preview gate", w.Code)
	}
}

type previewSink struct {
	heads []artifacts.PreviewRoute
	err   error
}

func (p *previewSink) PutPreview(_ context.Context, head artifacts.PreviewRoute) error {
	p.heads = append(p.heads, head)
	return p.err
}

func TestPreviewReconcilerRetriesExpiresAndProtectsArtifactCleanup(t *testing.T) {
	store := state.NewMemStore()
	account, site := routingSite(t, store, true)
	release, err := store.CreateHostingRelease(t.Context(), account.ID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 1, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReadyHostingRelease(t.Context(), account.ID, site.ID, release.ID); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	p, err := store.CreateHostingPreview(t.Context(), api.HostingPreview{ID: id, AccountID: account.ID, SiteID: site.ID, ReleaseID: release.ID, OriginID: site.OriginID, Hostname: "p-" + id + ".hosting.example", CreatedAt: now, ExpiresAt: now.Add(api.HostingPreviewTTLMin)})
	if err != nil {
		t.Fatal(err)
	}
	at := now.Add(time.Second)
	sink := &previewSink{err: errors.New("provider failure")}
	r := PreviewReconciler{Store: store, Sites: store, Publisher: sink, OriginID: site.OriginID, Now: func() time.Time { return at }}
	if err = r.RunOnce(t.Context()); err == nil {
		t.Fatal("lost publication failure")
	}
	rows, _ := store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if rows[0].PublishedGeneration != 0 || rows[0].LastErrorCode != api.HostingRouteErrorProvider {
		t.Fatal(rows)
	}
	sink.err = nil
	at = at.Add(api.HostingPreviewRetry + time.Second)
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if rows[0].PublishedGeneration != 1 {
		t.Fatal(rows)
	}
	// Site deletion revokes previews; byte GC must wait even with admission off.
	site, err = store.DeleteHostingSite(t.Context(), account.ID, site.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.HostingRouteSnapshot(t.Context(), site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.AcknowledgeHostingRoute(t.Context(), site.ID, snapshot.Generation, at.Add(-2*api.HostingRetirementGrace)); err != nil {
		t.Fatal(err)
	}
	gc := Reconciler{Store: store, Artifacts: &routeArtifacts{}, AdmissionEnabled: func() bool { return false }, Now: func() time.Time { return at.Add(time.Hour) }}
	if err = gc.reconcileSite(t.Context(), site); err != nil {
		t.Fatal(err)
	}
	if len(gc.Artifacts.(*routeArtifacts).removed) != 0 {
		t.Fatal("GC removed preview bytes")
	}
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if last := sink.heads[len(sink.heads)-1]; !last.Revoked || last.ReleaseID != p.ReleaseID {
		t.Fatal("missing immutable tombstone", last)
	}
	rows, _ = store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if len(rows) != 1 {
		t.Fatal("purged before edge grace")
	}
	at = at.Add(api.HostingPreviewRemovalGrace + time.Second)
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, _ = store.ListHostingPreviews(t.Context(), account.ID, site.ID)
	if len(rows) != 0 {
		t.Fatal("safe cleanup did not progress")
	}
	// Expiry is an independent revocation path, even without explicit deletion.
	_, site = routingSite(t, store, true)
	release, err = store.CreateHostingRelease(t.Context(), site.AccountID, site.ID, release.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = store.ReadyHostingRelease(t.Context(), site.AccountID, site.ID, release.ID)
	id = uuid.NewString()
	created := time.Now().UTC()
	p, err = store.CreateHostingPreview(t.Context(), api.HostingPreview{ID: id, AccountID: site.AccountID, SiteID: site.ID, ReleaseID: release.ID, OriginID: site.OriginID, Hostname: "p-" + id + ".hosting.example", CreatedAt: created, ExpiresAt: created.Add(api.HostingPreviewTTLMin)})
	if err != nil {
		t.Fatal(err)
	}
	at = p.ExpiresAt
	if err = r.RunOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !sink.heads[len(sink.heads)-1].Revoked {
		t.Fatal("expired preview was republished active")
	}
}
