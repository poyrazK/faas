package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us 2026-10-22: `gregale apps list` printed a parked app as
// "evicted_cold live", the stored status run into the deployment column.
func TestAppsListAlignsColumnsAndNamesParkedApps(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.AppResponse{
			{Slug: "alpha", Status: "active", URL: "https://alpha.gregale.dev", DeploymentAvailability: api.AppDeploymentAvailabilityLive},
			{Slug: "a-much-longer-app-slug-name", Status: "evicted_cold", URL: "https://a-much-longer-app-slug-name.gregale.dev", DeploymentAvailability: api.AppDeploymentAvailabilityLive},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	main.ResetJSONOut(t)
	buf, restore := main.CaptureStdout(t)
	code := main.CmdApps()
	restore()
	out := buf.String()
	if code != 0 {
		t.Fatalf("apps list = %d\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + 2 rows, got:\n%s", out)
	}
	if strings.Contains(out, "evicted_cold") || !strings.Contains(lines[2], "parked") {
		t.Fatalf("parked app not labelled parked:\n%s", out)
	}
	// Every column starts at the same offset on every line.
	for _, col := range []struct{ header, cell1, cell2 string }{
		{"STATUS", "active", "parked"},
		{"DEPLOYMENT", "live", "live"},
		{"URL", "https://alpha", "https://a-much"},
		{"AUTH", "", ""},
	} {
		at := strings.Index(lines[0], col.header)
		if col.cell1 == "" {
			continue
		}
		if strings.Index(lines[1], col.cell1) != at || strings.LastIndex(lines[2], col.cell2) != at {
			t.Fatalf("column %s not aligned at %d:\n%s", col.header, at, out)
		}
	}
}
