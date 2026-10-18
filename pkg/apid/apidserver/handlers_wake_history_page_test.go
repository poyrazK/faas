// adr: 1638
package apidserver_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"net/http"
	"testing"
)

func TestWakeHistoryPageHTTP(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	apidserver.MustSeedApp(t, e, "history-page")
	path := "/v1/apps/history-page/wake-timeline"
	for _, query := range []string{"", "?pagination=keyset_v1&limit=2"} {
		rec := e.Do(t, http.MethodGet, path+query, nil, nil)
		var out api.AppWakeTimelineResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatal("history read failed")
		}
		if query == "" && out.Page != nil || query != "" && (out.Page == nil || !out.Page.Complete || out.Page.SourceStatus != "read_succeeded" || rec.Header().Get("Cache-Control") != "no-store") {
			t.Fatal("compatibility or empty page metadata changed")
		}
	}
	for _, q := range []string{"?pagination=bad", "?cursor=bad", "?pagination=keyset_v1&limit=101", "?pagination=keyset_v1&since=bad"} {
		if r := e.Do(t, http.MethodGet, path+q, nil, nil); r.Code != 400 {
			t.Fatal("invalid page accepted")
		}
	}
	free := apidserver.Setup(t, api.PlanFree)
	if r := free.Do(t, http.MethodGet, path+"?pagination=keyset_v1", nil, nil); r.Code != 402 {
		t.Fatal("Free bypassed gate")
	}
	if r := e.Do(t, http.MethodGet, "/v1/apps/unknown/wake-timeline?pagination=keyset_v1", nil, nil); r.Code != 404 {
		t.Fatal("unowned app enumerated")
	}
}
