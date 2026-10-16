package hosting

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestHostingUsageIsAccountOwnedAndDefaultOff(t *testing.T) {
	store, site, _, _, _, _, _, _ := domainReconcilerFixture(t)
	host := &testHost{enabled: true, allowed: true, store: store, artifacts: &testArtifacts{}}
	service := Service{Host: host}
	read := func(account string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		service.Usage(w, httptest.NewRequest("GET", "/v1/internal/hosting/usage", nil), state.Account{ID: account})
		return w
	}
	if _, err := store.CreateHostingRelease(t.Context(), site.AccountID, site.ID, api.HostingManifest{Files: []api.HostingFile{{Path: "index.html", Size: 2, SHA256: strings.Repeat("a", 64), ContentType: "text/html"}}}); err != nil {
		t.Fatal(err)
	}
	w := read(site.AccountID)
	var usage api.HostingUsage
	if err := json.Unmarshal(w.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || usage.AccountID != site.AccountID || usage.ReservedBytes == 0 || usage.PendingUploads != 1 {
		t.Fatalf("usage: %d %s", w.Code, w.Body)
	}
	host.enabled = false
	if w := read(site.AccountID); w.Code != 503 {
		t.Fatalf("disabled usage: %d", w.Code)
	}
	host.enabled = true
	host.allowed = false
	if w := read(site.AccountID); w.Code != 403 {
		t.Fatalf("nonoperator usage: %d", w.Code)
	}
}
