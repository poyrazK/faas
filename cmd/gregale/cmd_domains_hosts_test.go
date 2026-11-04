// `gregale domain host set|get|clear` (ADR-520 amendment 2): the tree path
// reaches today's `domains host` command and each verb hits its route.

package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestDomainHostCommands(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		body       string
		status     int
		wantMethod string
		wantNames  []string
		wantOut    string
	}{
		{
			name: "set", args: []string{"domain", "host", "set", "*.example.com", "app", "eu.shop"},
			body: `{"domain":"*.example.com","names":["app","eu.shop"],"restricted":true}`, status: http.StatusOK,
			wantMethod: http.MethodPut, wantNames: []string{"app", "eu.shop"}, wantOut: "app, eu.shop",
		},
		{
			name: "get restricted", args: []string{"domain", "host", "get", "*.example.com"},
			body: `{"domain":"*.example.com","names":["app"],"restricted":true}`, status: http.StatusOK,
			wantMethod: http.MethodGet, wantOut: "  app\n",
		},
		{
			name: "show is get", args: []string{"domains", "host", "show", "*.example.com"},
			body: `{"domain":"*.example.com","names":[],"restricted":false}`, status: http.StatusOK,
			wantMethod: http.MethodGet, wantOut: "No allowlist",
		},
		{
			name: "clear", args: []string{"domain", "host", "clear", "*.example.com"},
			status: http.StatusNoContent, wantMethod: http.MethodDelete, wantOut: "again",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			previousOut := *main.OsStdout
			var out bytes.Buffer
			*main.OsStdout = &out
			t.Cleanup(func() { *main.OsStdout = previousOut })
			f := main.AuthedFakeAPI(t, tc.body, tc.status)
			if code := main.Run(tc.args); code != 0 {
				t.Fatalf("exit = %d, want 0; output:\n%s", code, out.String())
			}
			if *f.SawMethod() != tc.wantMethod || *f.SawPath() != "/v1/domains/*.example.com/hosts" {
				t.Errorf("route = %s %s, want %s /v1/domains/*.example.com/hosts", *f.SawMethod(), *f.SawPath(), tc.wantMethod)
			}
			if tc.wantNames != nil {
				var req api.SetCustomDomainHostsRequest
				if err := json.Unmarshal(*f.SawBody(), &req); err != nil || !reflect.DeepEqual(req.Names, tc.wantNames) {
					t.Errorf("request body %s (%v), want names %v", *f.SawBody(), err, tc.wantNames)
				}
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Errorf("output %q does not contain %q", out.String(), tc.wantOut)
			}
		})
	}
}

func TestDomainHostCommandsNeedTheirArguments(t *testing.T) {
	for _, args := range [][]string{
		{"domain", "host"},
		{"domain", "host", "set", "*.example.com"},
		{"domain", "host", "get"},
		{"domain", "host", "clear"},
		{"domain", "host", "bogus", "*.example.com"},
	} {
		main.ResetJSONOut(t)
		main.AuthedFakeAPI(t, "", http.StatusOK)
		if code := main.Run(args); code == 0 {
			t.Errorf("%v exited 0, want a usage error", args)
		}
	}
}
