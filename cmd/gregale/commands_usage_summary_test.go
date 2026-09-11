// Tests for the `gregale usage summary [--month YYYY-MM]` subcommand.
// Mirrors the `commands_crons_update_test.go` shape: httptest.NewServer
// fake + t.Setenv + osStdout swap.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// usageSummaryHandler returns a fake-apid that records the request
// path + query, then responds with the supplied summary payload.
// Used by every "happy" test below; error tests substitute their
// own handler.
func usageSummaryHandler(t *testing.T, payload api.UsageSummaryResponse) (*httptest.Server, *string, *string) {
	t.Helper()
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode(payload)
	}))
	return srv, &gotPath, &gotQuery
}

// --- happy paths ------------------------------------------------------------

func TestCmdUsageSummary_HappyPath_Default(t *testing.T) {
	srv, gotPath, gotQuery := usageSummaryHandler(t, api.UsageSummaryResponse{
		Month: "2026-07", UsedGBHours: 12.345, IncludedGBHours: 5,
		OverageGBHours: 7.345, OverageCents: 735,
	})
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsageSummary(nil); code != 0 {
		t.Errorf("summary default = %d, want 0", code)
	}
	if *gotPath != "/v1/usage/summary" {
		t.Errorf("path = %q, want /v1/usage/summary", *gotPath)
	}
	if *gotQuery != "" {
		t.Errorf("query = %q, want empty (default month)", *gotQuery)
	}
}

func TestCmdUsageSummary_HappyPath_ExplicitMonth(t *testing.T) {
	srv, gotPath, gotQuery := usageSummaryHandler(t, api.UsageSummaryResponse{
		Month: "2026-06", UsedGBHours: 1.0, IncludedGBHours: 5,
		OverageGBHours: 0, OverageCents: 0,
	})
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsageSummary([]string{"--month", "2026-06"}); code != 0 {
		t.Errorf("summary explicit month = %d, want 0", code)
	}
	if *gotPath != "/v1/usage/summary" {
		t.Errorf("path = %q, want /v1/usage/summary", *gotPath)
	}
	if *gotQuery != "month=2026-06" {
		t.Errorf("query = %q, want month=2026-06", *gotQuery)
	}
}

// --- client-side rejections -------------------------------------------------

func TestCmdUsageSummary_UnknownFlag(t *testing.T) {
	if code := cmdUsageSummary([]string{"--bogus"}); code != 1 {
		t.Errorf("unknown flag = %d, want 1", code)
	}
}

func TestCmdUsageSummary_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdUsageSummary(nil); code == 0 {
		t.Error("summary without token must fail")
	}
}

// --- server-side error surfacing --------------------------------------------

func TestCmdUsageSummary_BadMonth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.Problem{
			Type:   "about:blank",
			Title:  "Bad Request",
			Code:   api.CodeValidation,
			Status: http.StatusBadRequest,
			Detail: "month must match YYYY-MM",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdUsageSummary([]string{"--month", "not-a-month"}); code == 0 {
		t.Error("summary with bad month must fail")
	}
}

// --- JSON output ------------------------------------------------------------

func TestCmdUsageSummary_JSON_IndentedScalar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.UsageSummaryResponse{
			Month:           "2026-07",
			UsedGBHours:     12.345,
			IncludedGBHours: 5,
			OverageGBHours:  7.345,
			OverageCents:    735,
		})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsageSummary(nil); code != 0 {
		t.Fatalf("summary json = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "\n  ") {
		t.Fatalf("expected indented JSON, got %q", out)
	}
	var s api.UsageSummaryResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &s); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	// Pin every DTO field so a future rename or JSON-tag drift breaks
	// the test (same shape as TestCmdDeployment_JSON_SingleRecord).
	if s.Month != "2026-07" ||
		s.UsedGBHours != 12.345 ||
		s.IncludedGBHours != 5 ||
		s.OverageGBHours != 7.345 ||
		s.OverageCents != 735 {
		t.Errorf("JSON shape drift on UsageSummaryResponse; got %+v", s)
	}
}

// --- human output ----------------------------------------------------------

// TestCmdUsageSummary_HumanMultiLine pins the 5-row labelled block
// against the osStdout seam so a future refactor doesn't silently
// route the body through fmt.Printf (which bypasses the seam; same
// finding as PR #202 on the deployments list).
func TestCmdUsageSummary_HumanMultiLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.UsageSummaryResponse{
			Month: "2026-07", UsedGBHours: 12.345, IncludedGBHours: 5,
			OverageGBHours: 7.345, OverageCents: 735,
		})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsageSummary(nil); code != 0 {
		t.Fatalf("summary = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"Month:",
		"2026-07",
		"Used:",
		"12.345 GB-hours",
		"Included:",
		"5 GB-hours",
		"Overage:",
		"7.345 GB-hours",
		"Overage cost:",
		"735 cents",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("human block missing %q\nfull: %s", want, out)
		}
	}
}

// TestRenderUsageSummary_PinsColumnLayout pins the multi-line block
// against the writer seam (io.Writer). If the field set on
// UsageSummaryResponse grows or the column widths shift, this test
// surfaces the change.
func TestRenderUsageSummary_PinsColumnLayout(t *testing.T) {
	var buf bytes.Buffer
	renderUsageSummary(&buf, api.UsageSummaryResponse{
		Month:           "2026-07",
		UsedGBHours:     12.345,
		IncludedGBHours: 5,
		OverageGBHours:  7.345,
		OverageCents:    735,
		// Issue #279 / PR-B: the informational CPU panel.
		// 0.002778 CPU-hours × 3.6e9 µs/hour = ~10_000_080 µs,
		// i.e. 10 s of CPU consumed across the month — a
		// realistic order of magnitude for a Hobby app doing
		// bursty work, picked for a clean 4-decimal render.
		UsedCPUHours: 0.002778,
		// ADR-046: informational canonical interface egress
		// (net_tx_bytes, rolled up at the server). 1.234 GB is a non-zero
		// value so the line is exercised by this test.
		UsedEgressGB: 1.234,
	})
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 7 {
		t.Fatalf("line count = %d, want 7 (ADR-046 added Egress panel; issue #279 added CPU)\nraw: %s", len(lines), buf.String())
	}
	for _, want := range []string{
		"Month:", "2026-07",
		"Used:", "12.345 GB-hours",
		"Included:", "5 GB-hours",
		"Overage:", "7.345 GB-hours",
		"Overage cost:", "735 cents",
		"CPU usage:", "0.002778 CPU-hours",
		"Egress:", "1.234 GB",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("rendered output missing %q\nfull: %s", want, buf.String())
		}
	}
}

// --- dispatcher (back-compat) ----------------------------------------------

// TestCmdUsage_NoPositional_DispatchesToList pins the default branch:
// bare `gregale usage` must continue to call Client.GetUsage (per-app
// rows), not Client.UsageSummary. The fake-apid distinguishes the
// two routes by path; reaching the summary route would mean the
// dispatcher mis-routed.
func TestCmdUsage_NoPositional_DispatchesToList(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		switch r.URL.Path {
		case "/v1/usage":
			_ = json.NewEncoder(w).Encode([]api.UsageResponse{{
				AppID: "a1", Requests: 1, MBSeconds: 1, IncludedGBHours: 5,
			}})
		case "/v1/usage/summary":
			t.Errorf("dispatcher reached /v1/usage/summary for bare `gregale usage`; want /v1/usage")
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsage(nil); code != 0 {
		t.Errorf("cmdUsage no positional = %d, want 0", code)
	}
	if gotPath != "/v1/usage" {
		t.Errorf("path = %q, want /v1/usage (per-app list)", gotPath)
	}
}

// TestCmdUsage_FlagLeading_DispatchesToList pins the back-compat
// promise from the PR description: `gregale usage --month 2026-06`
// (the documented legacy invocation) must continue to work — the
// dispatcher forwards flag-leading args to cmdUsageList rather than
// rejecting them as "unknown usage subcommand". PR #220 review caught
// a regression here (strict dispatcher rejected --month as a
// positional); this test fails on the broken version and passes on
// the fix.
func TestCmdUsage_FlagLeading_DispatchesToList(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/v1/usage":
			_ = json.NewEncoder(w).Encode([]api.UsageResponse{{
				AppID: "a1", Requests: 1, MBSeconds: 1, IncludedGBHours: 5,
			}})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsage([]string{"--month", "2026-06"}); code != 0 {
		t.Errorf("cmdUsage --month 2026-06 = %d, want 0", code)
	}
	if gotPath != "/v1/usage" {
		t.Errorf("path = %q, want /v1/usage (per-app list, flag-forwarded)", gotPath)
	}
	if gotQuery != "month=2026-06" {
		t.Errorf("query = %q, want month=2026-06", gotQuery)
	}
}

// TestCmdUsage_UnknownSubcommand_ReturnsOne pins strict dispatch:
// `gregale usage bogus` must exit 1 with `unknown usage subcommand`
// on stderr. Matches cmdCrons / cmdDomains / cmdKeys conventions
// (PR #202 found this consistency gap and folded it in).
func TestCmdUsage_UnknownSubcommand_ReturnsOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	prev := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = prev }()

	if code := cmdUsage([]string{"bogus"}); code != 1 {
		t.Errorf("cmdUsage bogus = %d, want 1", code)
	}
	_ = w.Close()
	data, _ := io.ReadAll(r)
	if !strings.Contains(string(data), `unknown usage subcommand "bogus"`) {
		t.Errorf("stderr missing unknown-subcommand line\nfull: %s", data)
	}
}

// TestCmdUsage_SummarySubcommand_DispatchesToSummary pins that the
// "summary" positional routes through cmdUsageSummary, not the
// default per-app handler.
func TestCmdUsage_SummarySubcommand_DispatchesToSummary(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		switch r.URL.Path {
		case "/v1/usage/summary":
			_ = json.NewEncoder(w).Encode(api.UsageSummaryResponse{
				Month: "2026-07", UsedGBHours: 1, IncludedGBHours: 5,
				OverageGBHours: 0, OverageCents: 0,
			})
		case "/v1/usage":
			t.Errorf("dispatcher reached /v1/usage for `gregale usage summary`; want /v1/usage/summary")
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdUsage([]string{"summary"}); code != 0 {
		t.Errorf("cmdUsage summary = %d, want 0", code)
	}
	if gotPath != "/v1/usage/summary" {
		t.Errorf("path = %q, want /v1/usage/summary", gotPath)
	}
}

// --- run() dispatch --------------------------------------------------------

// TestCmdUsageList_HumanEgressColumn pins the ADR-046 egress column
// on the per-app usage list. When TXBytes or NetTxBytes is non-zero
// the human-mode line gains a trailing `· egress X.XXX GB (tx A.AA
// / net B.BB)` segment. When both are zero the original 4-field
// line is preserved (no trailing noise on a fresh account).
func TestCmdUsageList_HumanEgressColumn(t *testing.T) {
	cases := []struct {
		name    string
		tx, net int64
		wantSub []string
		wantNot []string
	}{
		{
			name: "zero egress omits column",
			tx:   0, net: 0,
			wantSub: []string{"a1 —", "1 · 0.000", "included 5", "App — requests"},
			// The header row says "egress" once; the row should NOT
			// contain a per-row egress column. The combined marker
			// "egress X.XXX GB" only appears when a row has a
			// non-zero egress cell.
			wantNot: []string{"egress 0.000 GB"},
		},
		{
			// 2.0 GiB net is canonical egress; 1.5 GiB tx is its diagnostic
			// payload subset and remains visible only in the split.
			// Conversion: float64(bytes) / (1024*1024*1024).
			name: "non-zero egress prints column",
			tx:   1610612736, net: 2147483648,
			wantSub: []string{"a1 —", "1 · 0.000", "included 5", "egress 2.000 GB", "tx 1.50", "net 2.00"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode([]api.UsageResponse{{
					AppID: "a1", Requests: 1, MBSeconds: 1, IncludedGBHours: 5,
					TXBytes: tc.tx, NetTxBytes: tc.net,
				}})
			}))
			defer srv.Close()

			var stdout bytes.Buffer
			oldOut := osStdout
			osStdout = &stdout
			defer func() { osStdout = oldOut }()

			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			if code := cmdUsageList(nil); code != 0 {
				t.Fatalf("list = %d, want 0", code)
			}
			out := stdout.String()
			for _, want := range tc.wantSub {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nfull: %s", want, out)
				}
			}
			for _, deny := range tc.wantNot {
				if strings.Contains(out, deny) {
					t.Errorf("output unexpectedly contained %q\nfull: %s", deny, out)
				}
			}
		})
	}
}

// TestRun_DispatchUsageSummary pins the main run() switch routes
// `usage summary` into cmdUsageSummary rather than mis-routing to
// the legacy per-app handler.
func TestRun_DispatchUsageSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/usage/summary":
			_ = json.NewEncoder(w).Encode(api.UsageSummaryResponse{Month: "2026-07"})
		default:
			http.Error(w, "no", 404)
		}
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if code := run([]string{"usage", "summary"}); code != 0 {
		t.Errorf("run usage summary = %d, want 0", code)
	}
}
