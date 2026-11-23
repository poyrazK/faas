package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// MCP Tasks prod re-probe 2026-11-23: after an MCP redeploy the previous
// release stays live at 0% so traffic can flip back, and the list showed two
// "live" releases. Beside a serving release it is a standby; a sole live
// release with no recorded share stays "live".
func TestReleaseListNamesFlipBackStandby(t *testing.T) {
	for _, tc := range []struct {
		name  string
		items []api.DeploymentResponse
		want  []string
	}{
		{"redeployed", []api.DeploymentResponse{
			{ID: "d2", AppID: "a1", Status: "live", TrafficPercent: 100, Kind: "app"},
			{ID: "d1", AppID: "a1", Status: "live", TrafficPercent: 0, Kind: "app"},
			{ID: "d0", AppID: "a1", Status: "superseded", Kind: "app"},
		}, []string{"live", "standby", "superseded"}},
		{"sole release without a share", []api.DeploymentResponse{
			{ID: "d1", AppID: "a1", Status: "live", Kind: "app"},
		}, []string{"live"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/apps/web/deployments" {
					_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{Items: tc.items})
					return
				}
				http.Error(w, `{"title":"boom"}`, http.StatusInternalServerError)
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			oldOut := *main.OsStdout
			t.Cleanup(func() { *main.OsStdout = oldOut })
			var out bytes.Buffer
			*main.OsStdout = &out
			if code := main.CmdDeployments([]string{"--app", "web"}); code != 0 {
				t.Fatalf("release list = %d\n%s", code, out.String())
			}
			var got []string
			for _, line := range strings.Split(out.String(), "\n") {
				if fields := strings.Fields(line); len(fields) >= 3 && fields[0] != "DEPLOYMENT" {
					got = append(got, fields[2])
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("statuses = %v, want %v\n%s", got, tc.want, out.String())
			}
		})
	}
}

// The MCP Tasks README reserves apps with `deploy --create-only` and then
// deploys them; the first deploy warned it would replace a running release
// that did not exist. An app without a live deployment has nothing to replace.
func TestUnlinkedGuardSkipsAppsWithoutALiveDeployment(t *testing.T) {
	oldErr := *main.OsStderr
	t.Cleanup(func() { *main.OsStderr = oldErr })
	for _, tc := range []struct {
		availability api.AppDeploymentAvailability
		wantNotice   bool
	}{
		{api.AppDeploymentAvailabilityMissing, false},
		{api.AppDeploymentAvailabilityLive, true},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1/apps/reserved" {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AppResponse{Slug: "reserved", DeploymentAvailability: tc.availability})
		}))
		var stderr bytes.Buffer
		*main.OsStderr = &stderr
		code := main.GuardUnlinkedExistingApp(context.Background(), api.NewClient(srv.URL, "fp_live_x"), "reserved", t.TempDir(), true)
		srv.Close()
		if code != 0 {
			t.Fatalf("%s: guard exit %d", tc.availability, code)
		}
		if got := strings.Contains(stderr.String(), "replaces its running release"); got != tc.wantNotice {
			t.Fatalf("%s: notice printed = %v, want %v (stderr %q)", tc.availability, got, tc.wantNotice, stderr.String())
		}
	}
}
