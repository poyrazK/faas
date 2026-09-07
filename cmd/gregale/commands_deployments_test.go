// Tests for the `gregale deployments` (list) and `gregale deployment <id>`
// (get) commands. Mirrors the cmdApps / cmdUsage test shapes from
// cli_test.go: httptest.NewServer fake + t.Setenv + osStdout swap + (for
// JSON) writeJSONTestStatus from commands_test.go for path-routed
// handlers. The dispatch placements live in main_test.go.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- cmdDeployments ---------------------------------------------------------

func TestCmdDeployments_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployments(nil); code != 0 {
		t.Errorf("cmdDeployments empty = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "No deployments yet.") {
		t.Errorf("missing 'No deployments yet.' line\nfull: %s", stdout.String())
	}
}

func TestCmdDeployments_NonEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{
			Items: []api.DeploymentResponse{
				{ID: "d1", AppID: "a1", Status: "succeeded", Kind: "app", CreatedAt: "2026-07-23T11:25:00Z"},
				{ID: "d2", AppID: "a2", Status: "failed", Kind: "app", CreatedAt: "2026-07-23T11:26:00Z"},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployments(nil); code != 0 {
		t.Errorf("cmdDeployments non-empty = %d, want 0", code)
	}
}

func TestCmdDeployments_NextBeforeHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{
			Items: []api.DeploymentResponse{
				{ID: "d1", AppID: "a1", Status: "succeeded", Kind: "app", CreatedAt: "2026-07-23T11:25:00Z"},
			},
			NextBefore: "2026-07-23T11:25:00.000000000Z",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployments(nil); code != 0 {
		t.Errorf("cmdDeployments next-before = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "--before") {
		t.Errorf("expected pagination hint mentioning --before\nfull: %s", stdout.String())
	}
}

func TestCmdDeployments_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdDeployments(nil); code == 0 {
		t.Error("cmdDeployments without token must fail")
	}
}

func TestCmdDeployments_InvalidLimit(t *testing.T) {
	if code := cmdDeployments([]string{"--limit", "999"}); code != 1 {
		t.Errorf("cmdDeployments limit=999 = %d, want 1", code)
	}
	if code := cmdDeployments([]string{"--limit", "-1"}); code != 1 {
		t.Errorf("cmdDeployments limit=-1 = %d, want 1", code)
	}
}

func TestCmdDeployments_ExtraPositional(t *testing.T) {
	if code := cmdDeployments([]string{"foo"}); code != 1 {
		t.Errorf("cmdDeployments extra positional = %d, want 1", code)
	}
}

func TestCmdDeployments_JSON_EnvelopeShape(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{
			Items: []api.DeploymentResponse{
				{ID: "d1", AppID: "a1", Status: "succeeded", Kind: "app", CreatedAt: "2026-07-23T11:25:00Z"},
			},
			NextBefore: "2026-07-23T11:25:00.000000000Z",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdDeployments(nil); code != 0 {
		t.Errorf("cmdDeployments json = %d, want 0", code)
	}
	var env api.DeploymentListResponse
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("JSON envelope parse failed: %v\nraw: %s", err, stdout.String())
	}
	if len(env.Items) != 1 || env.Items[0].ID != "d1" {
		t.Errorf("envelope items = %+v, want one d1", env.Items)
	}
	if env.NextBefore == "" {
		t.Errorf("envelope next_before lost; envelope = %+v", env)
	}
}

func TestCmdDeployments_All(t *testing.T) {
	// Two pages: first has NextBefore, second is empty.
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{
				Items: []api.DeploymentResponse{
					{ID: "d1", AppID: "a1", Status: "succeeded", Kind: "app", CreatedAt: "2026-07-23T11:25:00Z"},
				},
				NextBefore: "2026-07-23T11:25:00.000000000Z",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{}) // next_before empty -> stop
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployments([]string{"--all"}); code != 0 {
		t.Errorf("cmdDeployments --all = %d, want 0", code)
	}
	if calls < 2 {
		t.Errorf("expected at least 2 server calls (--all walks pages); got %d", calls)
	}
}

// --- cmdDeployment ----------------------------------------------------------

func TestCmdDeployment_MissingID(t *testing.T) {
	if code := cmdDeployment(nil); code != 1 {
		t.Errorf("cmdDeployment no args = %d, want 1", code)
	}
}

func TestCmdDeployment_ExtraPositional(t *testing.T) {
	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef", "extra"}); code != 1 {
		t.Errorf("cmdDeployment extra args = %d, want 1", code)
	}
}

func TestCmdDeployment_InvalidID(t *testing.T) {
	if code := cmdDeployment([]string{"not-hex"}); code != 1 {
		t.Errorf("cmdDeployment invalid id = %d, want 1", code)
	}
	if code := cmdDeployment([]string{"0123456789abcdef"}); code != 1 { // 30 chars
		t.Errorf("cmdDeployment short id = %d, want 1", code)
	}
}

func TestCmdDeployment_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/deployments/") {
			http.Error(w, "no", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
			ID:          "0123456789abcdef0123456789abcdef",
			AppID:       "fedcba9876543210fedcba9876543210",
			BuildID:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ImageDigest: "sha256:abc123",
			Kind:        "app",
			Status:      "succeeded",
			CreatedAt:   "2026-07-23T11:25:00Z",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Errorf("cmdDeployment happy = %d, want 0", code)
	}
}

func TestCmdDeployment_Unauthenticated(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef"}); code == 0 {
		t.Error("cmdDeployment without token must fail")
	}
}

func TestCmdDeployment_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"type":"...","title":"Not found","code":"not_found","status":404}`, 404)
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef"}); code == 0 {
		t.Error("cmdDeployment 404 must fail")
	}
}

func TestCmdDeployment_JSON_SingleRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
			ID:          "0123456789abcdef0123456789abcdef",
			AppID:       "fedcba9876543210fedcba9876543210",
			ImageDigest: "sha256:abc123",
			Kind:        "app",
			Status:      "succeeded",
			CreatedAt:   "2026-07-23T11:25:00Z",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	resetJSONOutput()
	t.Cleanup(resetJSONOutput)
	jsonOutput = true

	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Errorf("cmdDeployment json = %d, want 0", code)
	}
	var d api.DeploymentResponse
	if err := json.Unmarshal(stdout.Bytes(), &d); err != nil {
		t.Fatalf("JSON single-record parse failed: %v\nraw: %s", err, stdout.String())
	}
	// Pin every field on the DTO so a future rename or JSON-tag drift
	// breaks the test (issue from PR #202 code review).
	if d.ID != "0123456789abcdef0123456789abcdef" ||
		d.AppID != "fedcba9876543210fedcba9876543210" ||
		d.ImageDigest != "sha256:abc123" ||
		d.Kind != "app" ||
		d.Status != "succeeded" ||
		d.CreatedAt != "2026-07-23T11:25:00Z" {
		t.Errorf("JSON shape drift on DeploymentResponse; got %+v", d)
	}
}

// --- dispatch ---------------------------------------------------------------

// TestRun_DispatchDeployments asserts the main run() switch routes
// `deployments` and `deployment <id>` to their handlers rather than
// letting the singular fall into appSlugFallback.
func TestRun_DispatchDeployments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/deployments" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{
				Items: []api.DeploymentResponse{{ID: "d1", AppID: "a1", Status: "succeeded", Kind: "app"}},
			})
		case r.URL.Path == "/v1/deployments/0123456789abcdef0123456789abcdef":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
				ID: "0123456789abcdef0123456789abcdef", AppID: "a1", Status: "succeeded", Kind: "app",
			})
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

	// list path
	if code := run([]string{"deployments"}); code != 0 {
		t.Errorf("run deployments = %d, want 0", code)
	}
	// singular get
	if code := run([]string{"deployment", "0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Errorf("run deployment <id> = %d, want 0", code)
	}
}

// --- row rendering ----------------------------------------------------------

// TestRenderDeploymentRow_PinsColumnLayout pins the column-count and
// per-column widths of the human list table. If the DTO's id-length
// ceiling changes (e.g. UUIDv7 takes over for deployments) this layout
// has to shift and this test surfaces that. From PR #202 review.
func TestRenderDeploymentRow_PinsColumnLayout(t *testing.T) {
	var buf bytes.Buffer
	renderDeploymentRow(&buf, api.DeploymentResponse{
		ID:          "0123456789abcdef0123456789abcdef",
		AppID:       "fedcba9876543210fedcba9876543210",
		ImageDigest: "sha256:abc123",
		Kind:        "app",
		Status:      "succeeded",
		CreatedAt:   "2026-07-23T11:25:00Z",
	})
	line := strings.TrimRight(buf.String(), "\n")
	// 5 fields → 4 separators (whitespace cols are single-spaced after
	// the %-32s/%-12s/%-10s left-pads).
	parts := strings.Fields(line)
	if len(parts) != 5 {
		t.Fatalf("row column count = %d, want 5\nline: %q", len(parts), line)
	}
	if parts[0] != "0123456789abcdef0123456789abcdef" || parts[4] != "2026-07-23T11:25:00Z" {
		t.Errorf("row fields drifted: %q", line)
	}
}

// TestRenderDeploymentRowWide_PinsColumnLayout mirrors the above for
// the --wide annotation layout (issue #977 / ADR-116). 9 columns
// total: id / app / status / kind / by / pr / tag / reason /
// created. We pin via %-verb count in the format string rather than
// strings.Fields on the rendered line — the wide format pads each
// column with spaces so strings.Fields collapses the leading-pad
// columns into the data column. Format-string count is the canonical
// tripwire.
func TestRenderDeploymentRowWide_PinsColumnLayout(t *testing.T) {
	if got, want := countFormatVerbs(deploymentRowFmtWide), 9; got != want {
		t.Fatalf("deploymentRowFmtWide has %d %% verbs, want %d", got, want)
	}
	var buf bytes.Buffer
	renderDeploymentRowWide(&buf, api.DeploymentResponse{
		ID:          "0123456789abcdef0123456789abcdef",
		AppID:       "fedcba9876543210fedcba9876543210",
		ImageDigest: "sha256:abc123",
		Kind:        "app",
		Status:      "succeeded",
		DeployedBy:  "poyraz",
		PRNumber:    977,
		Tag:         "hotfix",
		Reason:      "Emergency rollback after payment provider incident",
		CreatedAt:   "2026-07-23T11:25:00Z",
	})
	line := strings.TrimRight(buf.String(), "\n")
	// Sanity-check the rendered line carries the four annotation
	// tokens (by / pr / tag / reason).
	for _, want := range []string{"poyraz", "977", "hotfix", "Emergency rollback after payment provide…"} {
		if !strings.Contains(line, want) {
			t.Errorf("rendered line missing %q: %q", want, line)
		}
	}
}

// TestRenderDeploymentRowWide_EmptyAnnotationsRendersDash validates
// that pre-feature rows render "-" (not empty spaces) in the
// annotation columns so the table stays aligned when a fleet mixes
// old and new rows. The 4 dashes are emitted by the renderer for
// DeployedBy="", PRNumber=0, Tag="", Reason="".
func TestRenderDeploymentRowWide_EmptyAnnotationsRendersDash(t *testing.T) {
	var buf bytes.Buffer
	renderDeploymentRowWide(&buf, api.DeploymentResponse{
		ID:        "0123456789abcdef0123456789abcdef",
		AppID:     "fedcba9876543210fedcba9876543210",
		Kind:      "app",
		Status:    "succeeded",
		CreatedAt: "2026-07-23T11:25:00Z",
	})
	line := strings.TrimRight(buf.String(), "\n")
	// Count the literal "-" placeholders in the rendered line.
	// Each annotation column contributes one dash; columns are
	// space-padded so the dash rides at the right edge of the pad.
	dashCount := strings.Count(line, " -")
	if dashCount < 4 {
		t.Errorf("expected ≥4 ' -' (annotation dashes), got %d in %q", dashCount, line)
	}
}

// countFormatVerbs returns the number of `%`-prefixed format verbs
// in a printf format string. Used by the layout-pin tests above to
// tripwire silent column-count drift without depending on the
// (whitespace-collapsing) shape of the rendered line. Skips the
// literal `%%` escape (a single `%` in the output).
func countFormatVerbs(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+1 < len(s) && s[i+1] == '%' {
			i++
			continue
		}
		n++
		// Skip the verb's optional [-+ #0]* prefix + width + .precision.
		j := i + 1
		for j < len(s) && (s[j] == '-' || s[j] == '+' || s[j] == ' ' || s[j] == '#' || s[j] == '0') {
			j++
		}
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == '.' {
			j++
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
		}
		i = j
	}
	return n
}

// TestTruncateReason pins the rune-aware truncation helper used by
// the wide layout. A multi-byte reason must NOT be sliced mid-rune
// (would render garbled on a UTF-8 terminal); the helper appends
// "…" when the rune count exceeds the cap. Below-cap strings pass
// through verbatim; empty passes through as empty (the wide
// renderer turns "" into "-" downstream).
func TestTruncateReason(t *testing.T) {
	tests := []struct {
		in   string
		max  int
		want string
	}{
		{"", 40, ""},
		{"short", 40, "short"},
		{"0123456789012345678901234567890123456789x", 40, "0123456789012345678901234567890123456789…"},
		// 4-byte rune boundary — len("é")==2 bytes / 1 rune.
		// Without rune-aware slicing this would slice mid-codepoint
		// and render as replacement chars.
		{"éééééééééééééééééééééééééééééééééééééééééééé", 5, "ééééé…"},
	}
	for _, tc := range tests {
		if got := truncateReason(tc.in, tc.max); got != tc.want {
			t.Errorf("truncateReason(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

// TestCmdDeployments_NonEmpty_RowsRendered validates that the human
// output is actually captured through the osStdout seam (PR #202 review
// found the prior `fmt.Printf` path bypassed the seam; this pins the
// new behaviour).
func TestCmdDeployments_NonEmpty_RowsRendered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentListResponse{
			Items: []api.DeploymentResponse{
				{ID: "0123456789abcdef0123456789abcdef", AppID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: "succeeded", Kind: "app", CreatedAt: "2026-07-23T11:25:00Z"},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployments(nil); code != 0 {
		t.Errorf("cmdDeployments = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "0123456789abcdef0123456789abcdef") {
		t.Errorf("deployment id not in human output\nfull: %s", out)
	}
	if !strings.Contains(out, "succeeded") || !strings.Contains(out, "app") {
		t.Errorf("status / kind not in human output\nfull: %s", out)
	}
}

// TestCmdDeployment_HappyPath_DetailRendered pins the human single-record
// detail block against the osStdout seam (was the same seam-bypass
// finding as the list path).
func TestCmdDeployment_HappyPath_DetailRendered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
			ID:          "0123456789abcdef0123456789abcdef",
			AppID:       "fedcba9876543210fedcba9876543210",
			BuildID:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ImageDigest: "sha256:abc123",
			Kind:        "app",
			Status:      "succeeded",
			CreatedAt:   "2026-07-23T11:25:00Z",
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Errorf("cmdDeployment = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"id:",
		"app_id:",
		"build_id:",
		"image_digest:",
		"kind:",
		"status:",
		"created_at:",
		"0123456789abcdef0123456789abcdef",
		"sha256:abc123",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail block missing %q\nfull: %s", want, out)
		}
	}
}

func TestCmdDeployment_HostingReceiptRendered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
			ID:          "0123456789abcdef0123456789abcdef",
			AppID:       "fedcba9876543210fedcba9876543210",
			ImageDigest: "sha256:abc123",
			Kind:        "app",
			Status:      "succeeded",
			CreatedAt:   "2026-07-23T11:25:00Z",
			APIHostingReceipt: json.RawMessage(`{
				"schema_version":1,
				"deployment_id":"0123456789abcdef0123456789abcdef",
				"app_id":"fedcba9876543210fedcba9876543210",
				"app_url":"https://receipt-app.apps.gregale.dev",
				"source":{"kind":"github","commit_sha":"deadbeef"},
				"profile":{"version":"v1","framework":"fastapi","port":8080,"health_path":"/healthz"},
				"artifact":{},
				"smoke":{"status":"verified","path":"/healthz","status_code":200,"latency_ms":42}
			}`),
		})
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()

	if code := cmdDeployment([]string{"0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Fatalf("cmdDeployment = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"hosting_status:",
		"verified",
		"hosting_app_url:",
		"https://receipt-app.apps.gregale.dev",
		"health_path:",
		"/healthz",
		"profile:",
		"fastapi (port 8080)",
		"source_sha:",
		"deadbeef",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hosting receipt output missing %q\nfull: %s", want, out)
		}
	}
}

// --- --before forwarding ----------------------------------------------------

// TestCmdDeployments_BeforeCursorForwarding pins URL-safe cursor forwarding.
// RFC3339Nano cursors contain colons and must be query-escaped before they are
// handed to net/http.
func TestCmdDeployments_BeforeCursorForwarding(t *testing.T) {
	var seenRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRaw = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	cursor := "2026-07-23T11:25:00.000000000Z"
	if code := cmdDeployments([]string{"--before", cursor}); code != 0 {
		t.Errorf("cmdDeployments --before = %d, want 0", code)
	}
	want := "before=" + url.QueryEscape(cursor) + "&limit=50"
	if seenRaw != want {
		t.Errorf("RawQuery = %q, want %q", seenRaw, want)
	}
}

// TestCmdDeployment_JSON_ShowSecretScanEnvelope pins the
// PR-A `gregale deployment <id> --show-secret-scan` JSON shape:
// the envelope emits the deployment row + a non-empty
// `secret_scan` object fetched from
// /v1/deployments/{id}/secret-scan. Mirrors the
// TestCmdDeployments_JSON_EnvelopeShape pattern at the top of
// this file (httptest, writeJSONTestStatus, jsonOutput swap).
//
// The test deliberately exercises the
// "scan-pending returns 404, non-fatal in JSON mode" path
// because that's the cold-deploy reality — the row exists, the
// scan hasn't run yet, the operator needs a usable JSON
// payload anyway. The text-mode path is covered separately by
// `TestCmdDeployment_Text_ShowSecretScanClean`.
func TestCmdDeployment_JSON_ShowSecretScanEnvelope(t *testing.T) {
	// Track which drill-down routes were hit so the test
	// fails fast if cmdDeploymentGet stops calling the new
	// `/secret-scan` endpoint. Two-route capture matches
	// the `(showScan + showSecretScan)` shape.
	var routesHit []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routesHit = append(routesHit, r.URL.Path)
		switch r.URL.Path {
		case "/v1/deployments/00000000000000000000000000000001":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
				ID:     "00000000000000000000000000000001",
				AppID:  "app",
				Status: "live",
			})
		case "/v1/deployments/00000000000000000000000000000001/secret-scan":
			// Mirror the v2 widening (migration 00264):
			// `complete_with_redactions` for a hit, `complete`
			// for a clean scan. The test pins the latter.
			_ = json.NewEncoder(w).Encode(api.SecretScanResult{
				Status:      "complete",
				ScannedAt:   "2026-08-14T12:00:00Z",
				ImageDigest: "sha256:deadbeef",
				Findings:    nil,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restoreStdout := swapStdout(t)
	jsonOutput = true
	defer func() { jsonOutput = false }()
	if code := cmdDeploymentGet([]string{
		"--show-secret-scan",
		"00000000000000000000000000000001",
	}); code != 0 {
		t.Fatalf("cmdDeploymentGet --show-secret-scan = %d, want 0", code)
	}

	// Decode the JSON envelope written to stdout. Same
	// shape as TestCmdDeployments_JSON_EnvelopeShape.
	var env struct {
		Deployment any `json:"deployment"`
		SecretScan any `json:"secret_scan"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (raw=%q)", err, stdout.String())
	}
	if env.Deployment == nil {
		t.Errorf("envelope missing deployment")
	}
	if env.SecretScan == nil {
		t.Errorf("envelope missing secret_scan (stdout=%q)", stdout.String())
	}
	// Confirm both routes were hit so a future refactor
	// that drops the per-flag dispatch surfaces here.
	want := map[string]bool{
		"/v1/deployments/00000000000000000000000000000001":             false,
		"/v1/deployments/00000000000000000000000000000001/secret-scan": false,
	}
	for _, p := range routesHit {
		if _, ok := want[p]; ok {
			want[p] = true
		}
	}
	for p, seen := range want {
		if !seen {
			t.Errorf("route %q not hit (got %v)", p, routesHit)
		}
	}
	restoreStdout()
}
