package hosting

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSiteStatusSeparatesIntentPublicationAndOwnership(t *testing.T) {
	h := &testHost{enabled: true, allowed: true, store: state.NewMemStore(), artifacts: &testArtifacts{}}
	account, site := routingSite(t, h.store, true)
	s := Service{Host: h}
	get := func(owner state.Account) (int, api.HostingSiteStatus) {
		t.Helper()
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		s.GetStatus(w, r, owner)
		var out api.HostingSiteStatus
		if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatal("invalid status JSON")
		}
		return w.Code, out
	}
	if code, _ := get(state.Account{ID: uuid.NewString()}); code != 404 {
		t.Fatalf("cross-account status: %d", code)
	}
	code, out := get(account)
	if code != 200 || out.Publication.State != "pending" || out.Publication.Generation != 1 || out.Publication.PublishedGeneration != 0 || out.Publication.PendingSince == nil {
		t.Fatalf("unpublished intent: %d %+v", code, out)
	}
	at := time.Now()
	if err := h.store.RetryHostingRoute(t.Context(), site.ID, 1, at, api.HostingRouteErrorProvider); err != nil {
		t.Fatal(err)
	}
	_, out = get(account)
	if out.Publication.RetryCount != 1 || out.Publication.LastErrorCode != api.HostingRouteErrorProvider || out.Publication.LastAttemptAt == nil || out.Publication.NextAttemptAt == nil || !out.Publication.NextAttemptAt.Equal(at.Add(api.HostingRouteRetryInterval)) {
		t.Fatalf("retry not visible: %+v", out)
	}
	if err := h.store.AcknowledgeHostingRoute(t.Context(), site.ID, 1, at); err != nil {
		t.Fatal(err)
	}
	_, out = get(account)
	if out.Publication.State != "published" || out.Publication.PendingSince != nil || out.Publication.PublishedAt == nil || out.Publication.RetryCount != 0 || out.Publication.LastErrorCode != "" {
		t.Fatalf("acknowledged status: %+v", out)
	}
	h.enabled = false
	if code, _ = get(account); code != 503 {
		t.Fatalf("flag bypass: %d", code)
	}
}

func TestLeasedStatusDoesNotInventProviderAcknowledgements(t *testing.T) {
	h := &testHost{enabled: true, allowed: true, store: state.NewMemStore(), artifacts: &testArtifacts{}}
	account, site := routingSite(t, h.store, false)
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("site", site.ID)
	w := httptest.NewRecorder()
	Service{Host: h}.GetStatus(w, r, account)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"state":"untracked"`) || strings.Contains(w.Body.String(), `"published_at"`) {
		t.Fatalf("fabricated lease publication receipt: %d %s", w.Code, w.Body)
	}
}
