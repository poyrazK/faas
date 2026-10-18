// adr: 1637
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWakeTimelineKeysetAPI(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "wake-paged", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	for _, kind := range []string{"wake.queue_accepted", "wake.admitted", "wake.boot_started", "wake.boot_completed"} {
		raw, _ := json.Marshal(map[string]string{"app_id": app.ID, "wake_id": "wake-1"})
		if err := (*e.Store()).AppendEventAt(t.Context(), "schedd", kind, nil, raw, at); err != nil {
			t.Fatal(err)
		}
	}
	path := "/v1/apps/wake-paged/wakes/wake-1/timeline"
	read := func(query string) api.WakeTimelineResponse {
		t.Helper()
		rec := e.Do(t, http.MethodGet, path+query, nil, nil)
		var out api.WakeTimelineResponse
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
			t.Fatalf("read=%d %s", rec.Code, rec.Body)
		}
		return out
	}
	legacy := read("?limit=2")
	if legacy.Page != nil || legacy.NextCursor == "" {
		t.Fatal("legacy timestamp contract changed")
	}
	legacyTail := read("?limit=2&since=" + url.QueryEscape(legacy.NextCursor))
	if len(legacyTail.Events) != 0 || legacyTail.Page != nil {
		t.Fatal("legacy exclusive timestamp filtering changed")
	}
	page := read("?pagination=keyset_v1&limit=2")
	if page.Page == nil || page.Page.Complete || len(page.Events) != 2 || page.NextCursor != "" {
		t.Fatal("missing opt-in page metadata")
	}
	last := read("?pagination=keyset_v1&limit=2&cursor=" + url.QueryEscape(page.Page.NextCursor))
	if last.Page == nil || !last.Page.Complete || last.Page.NextCursor != "" || len(last.Events) != 2 || last.Events[0].Kind != "wake.boot_started" || last.Events[0].At != page.Events[1].At {
		t.Fatal("API skipped equal-timestamp events")
	}
	for _, q := range []string{"?pagination=unknown", "?cursor=bad", "?pagination=keyset_v1&cursor=bad", "?pagination=keyset_v1&limit=3&cursor=" + url.QueryEscape(page.Page.NextCursor)} {
		rec := e.Do(t, http.MethodGet, path+q, nil, nil)
		if rec.Code != 400 {
			t.Fatalf("invalid page accepted: %d", rec.Code)
		}
	}
	free := apidserver.Setup(t, api.PlanFree)
	if rec := free.Do(t, http.MethodGet, path+"?pagination=keyset_v1", nil, nil); rec.Code != 402 {
		t.Fatal("Free plan bypassed gate")
	}
	foreign, err := (*e.Store()).CreateAccount(t.Context(), "foreign-wake-page@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*e.Store()).CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "foreign-wake-page"})
	if err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodGet, "/v1/apps/foreign-wake-page/wakes/wake-1/timeline?pagination=keyset_v1", nil, nil); rec.Code != 404 {
		t.Fatal("foreign ownership leaked")
	}
}
