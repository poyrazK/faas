package hosting

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type trafficFixture struct {
	calls        int
	site, origin string
	fail         bool
}

func (f *trafficFixture) Read(_ context.Context, site, origin string, now time.Time) (api.HostingTraffic, error) {
	f.calls++
	f.site, f.origin = site, origin
	if f.fail {
		return api.HostingTraffic{}, errors.New("provider SECRET")
	}
	return api.HostingTraffic{SiteID: site, Source: "cloudflare_analytics_engine", Approximate: true, ObservedAt: now}, nil
}
func TestTrafficReadRequiresSeparateGateAndAccountOwnership(t *testing.T) {
	store, site, _, _, _, _, _, _ := domainReconcilerFixture(t)
	reader := &trafficFixture{}
	h := &testHost{enabled: true, allowed: true, store: store, artifacts: &testArtifacts{}, trafficReader: reader}
	service := Service{Host: h}
	read := func(account string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.SetPathValue("site", site.ID)
		w := httptest.NewRecorder()
		service.GetTraffic(w, r, state.Account{ID: account})
		return w
	}
	if w := read(site.AccountID); w.Code != 503 || reader.calls != 0 {
		t.Fatalf("default gate: %d", w.Code)
	}
	h.analytics = true
	if w := read(uuid.NewString()); w.Code != 404 || reader.calls != 0 {
		t.Fatalf("foreign account queried analytics: %d", w.Code)
	}
	if w := read(site.AccountID); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || reader.site != site.ID || reader.origin != site.OriginID {
		t.Fatalf("owned query: %d %s", w.Code, w.Body)
	}
	reader.fail = true
	if w := read(site.AccountID); w.Code != 503 || strings.Contains(w.Body.String(), "SECRET") {
		t.Fatalf("provider failure: %d %s", w.Code, w.Body)
	}
	h.analytics = false
	if w := read(site.AccountID); w.Code != 503 || reader.calls != 2 {
		t.Fatal("disabled analytics reached provider")
	}
}
