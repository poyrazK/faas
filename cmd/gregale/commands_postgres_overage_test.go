// adr: 20261025094915 — gregale postgres overage shows and sets the compute
// overage choice, and stays out of discovery while its capability is hidden.
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestCmdPostgresOverage(t *testing.T) {
	const allowance = 75 * 3600
	enabled := false
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/postgres/overage" {
			http.NotFound(w, r)
			return
		}
		requests = append(requests, r.Method)
		if r.Method == http.MethodPut {
			var body map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode PUT body: %v", err)
			}
			enabled = body["enabled"]
		}
		ceiling := allowance
		if enabled {
			ceiling *= 2
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": enabled, "compute_allowance_unit_seconds": allowance, "compute_ceiling_unit_seconds": ceiling})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "compute overage off"},
		{[]string{"on"}, "compute_ceiling:              150.00 CU-hours"},
		{[]string{"off"}, "compute_ceiling:              75.00 CU-hours"},
	} {
		var out bytes.Buffer
		previousOut, previousJSON := (*main.OsStdout), (*main.JsonOutput)
		(*main.OsStdout), (*main.JsonOutput) = &out, false
		code := main.CmdPostgresOverage(tc.args)
		(*main.OsStdout), (*main.JsonOutput) = previousOut, previousJSON
		if code != 0 || !strings.Contains(out.String(), tc.want) {
			t.Fatalf("overage %v = %d:\n%s\nwant %q", tc.args, code, out.String(), tc.want)
		}
	}
	if strings.Join(requests, ",") != "GET,PUT,PUT" {
		t.Fatalf("requests %v", requests)
	}
	if code := main.CmdPostgresOverage([]string{"maybe"}); code == 0 {
		t.Fatal("an unknown argument was accepted")
	}
}

func TestPostgresOverageIsHiddenUntilReleased(t *testing.T) {
	view := &capabilitiesAPI{}
	server := httptest.NewServer(view)
	defer server.Close()
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "")
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	view.setHidden("postgres-overage")
	if _, help, _ := runCLI(t, "postgres", "--help"); !strings.Contains(help, "usage-import") || strings.Contains(help, "overage") {
		t.Fatalf("postgres help with overage hidden must list the rest and not overage:\n%s", help)
	}
	if code, _, stderr := runCLI(t, "postgres", "overage", "--help"); code == 0 || !strings.Contains(stderr, `unknown command "postgres overage"`) {
		t.Fatalf("hidden overage help = %d %q, want unknown command", code, stderr)
	}
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	view.setHidden()
	if _, help, _ := runCLI(t, "postgres", "--help"); !strings.Contains(help, "overage") {
		t.Fatalf("released postgres help must list overage:\n%s", help)
	}
}
