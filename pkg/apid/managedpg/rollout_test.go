// adr: 1027 — in-place major upgrades are a preview: nobody sees them until
// listed, and outside the list their routes answer like unregistered ones.
package managedpg

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	listed         = "6f1d2c3b-4a5e-4f60-8172-93a4b5c6d7e8"
	testRolloutEnv = "TEST_ROLLOUT_ACCOUNTS"
)

func TestParseRollout(t *testing.T) {
	off, err := ParseRollout(testRolloutEnv, " ")
	if err != nil || !off.Off() || off.Allows(listed) {
		t.Fatalf("empty = %+v, %v; want nobody", off, err)
	}
	some, err := ParseRollout(testRolloutEnv, " "+strings.ToUpper(listed)+" ,")
	if err != nil || some.Off() || !some.Allows(listed) || some.Allows("8a7b6c5d-4e3f-4a2b-9c1d-0e1f2a3b4c5d") || some.Allows("not-a-uuid") {
		t.Fatalf("one account = %+v, %v", some, err)
	}
	all, err := ParseRollout(testRolloutEnv, listed+",*")
	if err != nil || !all.Allows("8a7b6c5d-4e3f-4a2b-9c1d-0e1f2a3b4c5d") {
		t.Fatalf("everyone = %+v, %v", all, err)
	}
	if _, err := ParseRollout(testRolloutEnv, "acct-1"); err == nil || !strings.Contains(err.Error(), testRolloutEnv) {
		t.Fatalf("an invalid entry = %v; want an error naming the variable", err)
	}
}

func TestIsUpgradePath(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/postgres/databases/db1/upgrade":           true,
		"/v1/postgres/databases/db1/upgrades/u1":       true,
		"/v1/postgres/databases/db1/upgrades/":         false,
		"/v1/postgres/databases/db1/upgrades/u1/x":     false,
		"/v1/postgres/databases//upgrade":              false,
		"/v1/postgres/databases/db1/resize":            false,
		"/v1/postgres/databases/db1/upgrade/extra":     false,
		"/v1/postgres/databases/db1":                   false,
		"/v1/apps/x/upgrade":                           false,
		"/v1/postgres/databases/db1/compute-policy":    false,
		"/v1/postgres/databases/db1/upgrade-something": false,
	} {
		if IsUpgradePath(path) != want {
			t.Errorf("IsUpgradePath(%q) = %v, want %v", path, !want, want)
		}
	}
}

func TestRolloutHidesLikeAnUnregisteredRoute(t *testing.T) {
	nobody, _ := ParseRollout(testRolloutEnv, "")
	one, _ := ParseRollout(testRolloutEnv, listed)
	everyone, _ := ParseRollout(testRolloutEnv, "*")
	reached := 0
	serve := func(r Rollout, account string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		inner := RolloutAccount(func() Rollout { return r }, func(http.ResponseWriter, *http.Request, state.Account) { reached++ })
		route := RolloutRoute(func() Rollout { return r }, func(w http.ResponseWriter, req *http.Request) { inner(w, req, state.Account{ID: account}) })
		route(w, httptest.NewRequest(http.MethodPost, "/v1/postgres/databases/db1/upgrade", nil))
		return w
	}
	for name, w := range map[string]*httptest.ResponseRecorder{
		"nobody listed":      serve(nobody, listed),
		"account not listed": serve(one, "8a7b6c5d-4e3f-4a2b-9c1d-0e1f2a3b4c5d"),
	} {
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no such API route") {
			t.Errorf("%s = %d %s", name, w.Code, w.Body.String())
		}
	}
	serve(one, listed)
	serve(everyone, "8a7b6c5d-4e3f-4a2b-9c1d-0e1f2a3b4c5d")
	if reached != 2 {
		t.Fatalf("listed and launched accounts reached the handler %d times, want 2", reached)
	}
	req := httptest.NewRequest(http.MethodPut, "/v1/postgres/databases/db1/upgrade", nil)
	if !one.HidesUpgrade(req) || everyone.HidesUpgrade(req) || one.HidesUpgrade(httptest.NewRequest(http.MethodPut, "/v1/postgres/databases/db1/resize", nil)) {
		t.Fatal("method mismatches on upgrade paths must answer as missing until launch, and only those")
	}
}
