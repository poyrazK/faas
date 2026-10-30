package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261019071733 amendment 4 — `--org` reaches a company organization's
// freezes, `--app` the routes that hold an app's freezes whoever owns them,
// and `--all-apps` the account's freezes on its personal apps.
func TestFreezeCommandsReachTheOwnersRoutes(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ListReleaseFreezesResponse{Freezes: []api.ReleaseFreeze{{ID: "f1", Org: "acme", Kind: "window", Reason: "holiday"}}})
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.ReleaseFreeze{ID: "f2", Kind: "window", Reason: "holiday"})
		default:
			_ = json.NewEncoder(w).Encode(api.ReleaseFreeze{ID: "f1", Kind: "window", Reason: "holiday"})
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	main.WithResetJSONOutput(t, false)

	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"create", "--org", "acme", "--for", "2h", "--reason", "holiday"}, "POST /v1/orgs/acme/freezes"},
		{[]string{"create", "--app", "acme-api", "--for", "2h", "--reason", "holiday"}, "POST /v1/apps/acme-api/freezes"},
		{[]string{"create", "--all-apps", "--for", "2h", "--reason", "holiday"}, "POST /v1/freezes"},
		{[]string{"list", "--org", "acme"}, "GET /v1/orgs/acme/freezes"},
		{[]string{"list", "--app", "acme-api"}, "GET /v1/apps/acme-api/freezes"},
		{[]string{"list"}, "GET /v1/freezes"},
		{[]string{"lift", "f1", "--org", "acme"}, "DELETE /v1/orgs/acme/freezes/f1"},
		{[]string{"lift", "f1", "--app", "acme-api"}, "DELETE /v1/apps/acme-api/freezes/f1"},
		{[]string{"lift", "f1"}, "DELETE /v1/freezes/f1"},
	} {
		got = nil
		stdout, stderr, restore := main.SwapIO(t)
		code := main.Run(append([]string{"release", "freeze"}, tc.args...))
		restore()
		if code != 0 || len(got) != 1 || got[0] != tc.want {
			t.Errorf("freeze %s: exit %d, requests %v, want [%s]\nstdout: %s\nstderr: %s", strings.Join(tc.args, " "), code, got, tc.want, stdout, stderr())
		}
	}

	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"release", "freeze", "create", "--org", "acme", "--all-apps", "--for", "2h", "--reason", "x"})
	restore()
	if code == 0 || !strings.Contains(stderr(), "use one of --app, --all-apps and --org") {
		t.Fatalf("--org with --all-apps: exit %d, stderr %s", code, stderr())
	}
}
