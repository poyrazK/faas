package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adr: 20261016151947 — live queries are invisible outside FAAS_LIVE_QUERIES_ACCOUNTS:
// every live route, any method, with or without credentials, answers like an
// unregistered route, and the account's realtime endpoints get no live
// routes. A listed account reaches the routes.
func TestLiveQueriesHiddenOutsideRollout(t *testing.T) {
	e := entityAPISetup(t)
	requests := func() []*httptest.ResponseRecorder {
		return []*httptest.ResponseRecorder{
			e.do(t, http.MethodGet, "/v1/postgres/databases/db_1/live", nil, nil),
			e.do(t, http.MethodPost, "/v1/postgres/databases/db_1/live", nil, nil),
			e.do(t, http.MethodPost, "/v1/postgres/databases/db_1/live/rotate", nil, nil),
			e.do(t, http.MethodDelete, "/v1/postgres/databases/db_1/live", nil, nil),
			e.do(t, http.MethodPut, "/v1/postgres/databases/db_1/live", nil, nil),
		}
	}
	// Unconfigured, and configured with no accounts: nobody.
	for _, res := range append(requests(), anonymousEntityRequest(e, "/v1/postgres/databases/db_1/live")) {
		assertUnregisteredRoute(t, res)
	}
	if err := e.s.ConfigureLiveQueriesForTest(func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, res := range requests() {
		assertUnregisteredRoute(t, res)
	}

	// Another account listed: still hidden from this one.
	other := "00000000-0000-4000-8000-000000000001"
	if err := e.s.ConfigureLiveQueriesForTest(liveRolloutEnv(other)); err != nil {
		t.Fatal(err)
	}
	for _, res := range requests() {
		assertUnregisteredRoute(t, res)
	}
	if routes, err := e.s.LiveRoutesForTest(t.Context(), e.acct.ID, "app-1"); err != nil || len(routes) != 0 {
		t.Fatalf("live routes outside the rollout = %v, %v", routes, err)
	}

	// This account listed: the routes answer (unavailable without managed
	// Postgres here), and a wrong method is still hidden.
	if err := e.s.ConfigureLiveQueriesForTest(liveRolloutEnv(other + "," + e.acct.ID)); err != nil {
		t.Fatal(err)
	}
	res := e.do(t, http.MethodGet, "/v1/postgres/databases/db_1/live", nil, nil)
	if res.Code == http.StatusNotFound || !strings.Contains(res.Body.String(), "live_queries_unavailable") {
		t.Fatalf("listed account = %d %s", res.Code, res.Body.String())
	}
	assertUnregisteredRoute(t, e.do(t, http.MethodPut, "/v1/postgres/databases/db_1/live", nil, nil))

	if err := e.s.ConfigureLiveQueriesForTest(liveRolloutEnv("acct-1")); err == nil {
		t.Fatal("a non-UUID account was accepted")
	}
}

func liveRolloutEnv(accounts string) func(string) string {
	return func(key string) string {
		if key == "FAAS_LIVE_QUERIES_ACCOUNTS" {
			return accounts
		}
		return ""
	}
}
