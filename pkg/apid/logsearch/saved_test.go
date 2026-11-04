package logsearch

// adr: 20261018083536 — saved searches store canonical filters; ?saved=NAME
// applies them under any filter the request sets itself.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func serveSaved(t *testing.T, h func(http.ResponseWriter, *http.Request, state.Account), acct state.Account, method, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "/v1/apps/shop/log-searches/"+name, strings.NewReader(body))
	r.SetPathValue("slug", "shop")
	r.SetPathValue("name", name)
	w := httptest.NewRecorder()
	h(w, r, acct)
	return w
}

func TestSavedSearchLifecycle(t *testing.T) {
	svc, acct := newService(t, true)
	w := serveSaved(t, svc.PutSaved, acct, http.MethodPut, "checkout-5xx", `{"query":"status=5xx&route=/checkout&q="}`)
	var saved api.LogSavedSearch
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &saved) != nil || saved.Query != "route=%2Fcheckout&status=5xx" {
		t.Fatalf("put = %d %s, want the canonical query", w.Code, w.Body.String())
	}
	for name, body := range map[string]string{
		"window":      `{"query":"since=1h"}`,
		"bad level":   `{"query":"level=loud"}`,
		"not json":    `query=status`,
		"repeat keys": `{"query":"level=error&level=warn"}`,
	} {
		if w := serveSaved(t, svc.PutSaved, acct, http.MethodPut, "bad", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: put = %d, want 400", name, w.Code)
		}
	}
	if w := serveSaved(t, svc.PutSaved, acct, http.MethodPut, "Bad_Name", `{"query":"level=error"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("bad name = %d, want 400", w.Code)
	}

	var list api.LogSavedSearchesResponse
	if code := serve(t, svc.ListSaved, acct, "/v1/apps/shop/log-searches", &list); code != http.StatusOK || len(list.Searches) != 1 || list.Searches[0].Name != "checkout-5xx" {
		t.Fatalf("list = %d %+v", code, list)
	}

	// The saved filters apply; the request's own status overrides theirs.
	var page api.LogEventsResponse
	if code := serve(t, svc.Search, acct, "/v1/apps/shop/log-events?saved=checkout-5xx", &page); code != http.StatusOK || len(page.Events) != 1 || page.Events[0].Status != 502 {
		t.Fatalf("saved search = %d %+v, want the 502 only", code, page.Events)
	}
	if code := serve(t, svc.Search, acct, "/v1/apps/shop/log-events?saved=checkout-5xx&status=2xx", &page); code != http.StatusOK || len(page.Events) != 1 || page.Events[0].Status != 200 {
		t.Fatalf("saved search with an override = %d %+v, want the 200 only", code, page.Events)
	}
	if code := serve(t, svc.Facets, acct, "/v1/apps/shop/log-events/facets?saved=missing", nil); code != http.StatusNotFound {
		t.Fatalf("unknown saved search = %d, want 404", code)
	}

	if w := serveSaved(t, svc.DeleteSaved, acct, http.MethodDelete, "checkout-5xx", ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := serveSaved(t, svc.DeleteSaved, acct, http.MethodDelete, "checkout-5xx", ""); w.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", w.Code)
	}
}

func TestSavedSearchQuotaAndGate(t *testing.T) {
	svc, acct := newService(t, true)
	for i := range api.LogSavedSearchesPerApp {
		name := "s" + strings.Repeat("x", i%3) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if w := serveSaved(t, svc.PutSaved, acct, http.MethodPut, name, `{"query":"level=error"}`); w.Code != http.StatusOK {
			t.Fatalf("put %d (%s) = %d %s", i, name, w.Code, w.Body.String())
		}
	}
	w := serveSaved(t, svc.PutSaved, acct, http.MethodPut, "one-too-many", `{"query":"level=warn"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "log_saved_search_quota_exceeded") {
		t.Fatalf("past the cap = %d %s, want 409 log_saved_search_quota_exceeded", w.Code, w.Body.String())
	}
	off, acct := newService(t, false)
	if w := serveSaved(t, off.PutSaved, acct, http.MethodPut, "x", `{"query":"level=error"}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled put = %d, want 503", w.Code)
	}
}
