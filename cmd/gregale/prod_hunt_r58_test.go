package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Prod hunt r58 (rc.381): `egress-allowlist remove` of an entry that was
// never on the list said "updated", and `add` of one already there printed
// nothing. Entries compare as the server stores them (1.1.1.1 is
// 1.1.1.1/32).
func TestEgressAllowlistRemoveAbsentAndAddPresent(t *testing.T) {
	patched := 0
	linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/web":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "web", EgressAllowlist: []string{"1.1.1.1/32"}})
			return true
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/apps/web":
			patched++
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "web"})
			return true
		}
		return false
	})
	code, stdout, stderr := runCLI(t, "app", "web", "egress-allowlist", "remove", "9.9.9.9/32")
	if code != 0 || !strings.Contains(stdout, "9.9.9.9/32 is not in web's egress allowlist; nothing changed") {
		t.Fatalf("remove absent = %d:\n%s%s", code, stdout, stderr)
	}
	code, stdout, stderr = runCLI(t, "app", "web", "egress-allowlist", "add", "1.1.1.1")
	if code != 0 || !strings.Contains(stdout, "1.1.1.1/32 is already in web's egress allowlist") {
		t.Fatalf("add present = %d:\n%s%s", code, stdout, stderr)
	}
	if patched != 0 {
		t.Fatalf("no-op edits sent %d PATCH requests", patched)
	}
	if code, stdout, stderr = runCLI(t, "app", "web", "egress-allowlist", "remove", "1.1.1.1"); code != 0 || patched != 1 {
		t.Fatalf("remove held entry by address = %d (patched %d):\n%s%s", code, patched, stdout, stderr)
	}
}

// Prod hunt r58: after `app rename` the old hostname answered 404 at once,
// which only the help said; the result now names both URLs.
func TestAppRenameSaysTheOldURLStopsRouting(t *testing.T) {
	linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/apps/web/rename" {
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "web-v2", URL: "https://web-v2.gregale.app"})
			return true
		}
		return false
	})
	code, stdout, stderr := runCLI(t, "app", "rename", "web", "web-v2")
	if code != 0 || !strings.Contains(stdout, "Now serving at https://web-v2.gregale.app; https://web.gregale.app no longer routes.") {
		t.Fatalf("rename = %d:\n%s%s", code, stdout, stderr)
	}
}

// Prod hunt r58: `app network show` printed the hostname form
// `<app-slug>.svc.gregale`, and `network doctor` warned that private networks
// and upstream telemetry were broken on every app of an installation that
// does not offer them, with fixes the customer cannot apply.
func TestNetworkDoctorSkipsFeaturesNotOffered(t *testing.T) {
	problem := func(w http.ResponseWriter, status int, code string) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(api.Problem{Status: status, Code: code, Title: "Unavailable", Detail: code + ": not on this installation"})
	}
	linkedAppServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/v1/apps/web":
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "web", Status: "active"})
		case "/v1/apps/web/static-egress-ip":
			_ = json.NewEncoder(w).Encode(api.AppStaticEgressIPResponse{PlanAllowed: true})
		case "/v1/apps/web/network/private":
			problem(w, http.StatusServiceUnavailable, api.CodePrivateNetworkNotEnabled)
		case "/v1/apps/web/upstreams":
			problem(w, http.StatusServiceUnavailable, api.CodeDataUpstreamsDisabled)
		default:
			return false
		}
		return true
	})
	_, stdout, stderr := runCLI(t, "app", "web", "network", "show")
	if !strings.Contains(stdout, "web.svc.gregale:10081") || strings.Contains(stdout, "<app-slug>") {
		t.Fatalf("network show does not name the app's address:\n%s%s", stdout, stderr)
	}
	_, stdout, stderr = runCLI(t, "app", "web", "network", "doctor")
	for _, want := range []string{"private network attachments are not offered", "upstream telemetry is not offered"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("network doctor missing %q:\n%s%s", want, stdout, stderr)
		}
	}
	if strings.Contains(stdout, "Enable the private-network attachment surface") || strings.Contains(stdout, "Retry after the upstream API") {
		t.Fatalf("network doctor still gives a fix for a feature not offered:\n%s", stdout)
	}
}
