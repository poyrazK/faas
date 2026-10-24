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

// `gregale usage` listed 200+ rows in app-id order with no total (Hunt 12
// H12-2). Rows now lead with the largest GB-hours and end with a total.
func TestCmdUsageListSortsAndTotals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/apps" {
			_ = json.NewEncoder(w).Encode([]api.AppResponse{})
			return
		}
		_ = json.NewEncoder(w).Encode([]api.UsageResponse{
			{AppID: "small", Requests: 9, MBSeconds: 3_600_000, IncludedGBHours: 3000},
			{AppID: "big", Requests: 1, MBSeconds: 36_000_000, IncludedGBHours: 3000},
			{AppID: "mid", Requests: 5, MBSeconds: 7_200_000, IncludedGBHours: 3000},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	var stdout bytes.Buffer
	old := *main.OsStdout
	*main.OsStdout = &stdout
	defer func() { *main.OsStdout = old }()
	if code := main.CmdUsageList(nil); code != 0 {
		t.Fatalf("usage exit = %d", code)
	}
	out := stdout.String()
	big, mid, small := strings.Index(out, "big —"), strings.Index(out, "mid —"), strings.Index(out, "small —")
	if big < 0 || mid < 0 || small < 0 || !(big < mid && mid < small) {
		t.Fatalf("rows not ordered by GB-hours:\n%s", out)
	}
	if !strings.Contains(out, "Total: 13.000 GB-hours across 3 apps") {
		t.Fatalf("missing total:\n%s", out)
	}
}
