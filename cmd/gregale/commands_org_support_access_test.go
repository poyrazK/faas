package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// supportAccessCLI serves the org support-access routes and records the last
// request it got.
func supportAccessCLI(t *testing.T, gotMethod, gotPath *string) *bytes.Buffer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotMethod, *gotPath = r.Method, r.URL.Path
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(api.SupportAccess{
			Enabled: true,
			Grants: []api.SupportGrant{
				{ID: "sg_1", Org: "acme", Apps: []string{"web", "api"}, Status: api.SupportGrantActive, ExpiresAt: "2026-11-18T10:00:00Z", CreatedBy: "owner@acme.test"},
				{ID: "sg_0", Org: "acme", Apps: []string{}, AllApps: true, Status: api.SupportGrantRevoked, ExpiresAt: "2026-11-10T10:00:00Z", CreatedBy: "owner@acme.test"},
			},
			Requests: []api.SupportAccessRequest{
				{ID: "sr_1", Org: "acme", Operator: "Gregale support", Reason: "retry the failed build", Status: "open", ExpiresAt: "2026-11-18T09:00:00Z"},
				{ID: "sr_0", Org: "acme", Operator: "Gregale support", Reason: "old", Status: "declined", ExpiresAt: "2026-11-09T09:00:00Z"},
			},
		})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	resetJSONEnv(t)
	var out bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &out
	t.Cleanup(func() { *main.OsStdout = old })
	return &out
}

func TestCmdOrgSupportAccess(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
		wantOut    []string
		notOut     []string
	}{
		{
			name: "list", args: []string{"list", "--org", "acme"},
			wantMethod: http.MethodGet, wantPath: "/v1/orgs/acme/support-access",
			wantOut: []string{"Request sr_1 from Gregale support: retry the failed build", "sg_1", "web,api", "(all)", "owner@acme.test"},
			notOut:  []string{"sr_0"},
		},
		{
			name: "revoke", args: []string{"revoke", "--org", "acme", "sg_1"},
			wantMethod: http.MethodPost, wantPath: "/v1/orgs/acme/support-access/grants/sg_1/revoke",
			wantOut: []string{"Revoked."},
		},
		{
			name: "reject", args: []string{"reject", "--org", "acme", "sr_1"},
			wantMethod: http.MethodPost, wantPath: "/v1/orgs/acme/support-access/requests/sr_1/decline",
			wantOut: []string{"Rejected."},
		},
		{
			name: "approve points at the dashboard", args: []string{"approve", "--org", "acme"},
			wantOut: []string{"/dashboard/support-access"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var method, path string
			out := supportAccessCLI(t, &method, &path)
			if code := main.CmdOrgs(append([]string{"support-access"}, tc.args...)); code != 0 {
				t.Fatalf("exit = %d, output %q", code, out.String())
			}
			if method != tc.wantMethod || path != tc.wantPath {
				t.Fatalf("request = %s %s, want %s %s", method, path, tc.wantMethod, tc.wantPath)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
			for _, not := range tc.notOut {
				if strings.Contains(out.String(), not) {
					t.Errorf("output has %q:\n%s", not, out.String())
				}
			}
		})
	}
}

func TestCmdOrgSupportAccessUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"list"},
		{"revoke", "--org", "acme"},
		{"list", "--org", "acme", "extra"},
		{"grant", "--org", "acme"},
	} {
		var method, path string
		supportAccessCLI(t, &method, &path)
		if code := main.CmdOrgs(append([]string{"support-access"}, args...)); code == 0 {
			t.Errorf("%v: exit 0, want usage error", args)
		}
		if method != "" {
			t.Errorf("%v: sent %s %s", args, method, path)
		}
	}
}
