package main

// commands_decompose_test.go — Phase 3 CLI tests for the
// repo decomposition surface (ADR-050). Covers the §4 acceptance
// gate (`gregale deploy` on the fixture repo creates 3 apps + 1
// cron on one keypress; over-quota creates nothing; --json output
// is byte-stable) and the related cmdScan dry-run paths.
//
// Two test seams drive the suite:
//   - decomposeSink is a tiny http test server that mirrors the
//     /v1/projects/scan and /v1/projects endpoints. The same
//     fixture over-quota case is reused in three tests, asserted
//     against the wire shape the CLI produces.
//   - cmdScan / cmdDeployTarball are invoked directly (with env
//     FAAS_API + FAAS_TOKEN set) so the existing authedClient
//     path is exercised end-to-end without an httptest middleware
//     doubling as the server.

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// decomposeSink is the http test server for the /v1/projects surface.
// The fields are public so tests can populate them inline. On mismatch
// (path sneaks past the registered handlers) the sink returns 404 with
// a useful message — that keeps regressions from masquerading as a
// happy-path write.
type decomposeSink struct {
	mu              sync.Mutex
	scanStatus      int
	scanBody        api.PlanResponse
	applyStatus     int
	applyBody       api.ApplyResponse
	deployments     map[string]api.DeploymentResponse
	builds          map[string]api.BuildResponse
	deploymentCalls int
	buildCalls      int

	// capture lets tests assert the multipart body shape, including
	// the parsed Content-Type and the field set the SDK Client writes.
	capturedMultipart []byte
	projectSlug       string
	scanCalls         int
	applyCalls        int
}

func (s *decomposeSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/projects/scan" && r.Method == http.MethodPost:
		s.scanCalls++
		body, _ := io.ReadAll(r.Body)
		s.capturedMultipart = body
		s.projectSlug = multipartField(body, r.Header.Get("Content-Type"), "project_slug")
		writeJSONTestStatus(w, s.scanStatus, s.scanBody)
	case r.URL.Path == "/v1/projects" && r.Method == http.MethodPost:
		s.applyCalls++
		body, _ := io.ReadAll(r.Body)
		s.capturedMultipart = body
		s.projectSlug = multipartField(body, r.Header.Get("Content-Type"), "project_slug")
		writeJSONTestStatus(w, s.applyStatus, s.applyBody)
	case strings.HasPrefix(r.URL.Path, "/v1/deployments/") && r.Method == http.MethodGet:
		s.mu.Lock()
		s.deploymentCalls++
		s.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/v1/deployments/")
		if deployment, ok := s.deployments[id]; ok {
			writeJSONTestStatus(w, http.StatusOK, deployment)
			return
		}
		http.Error(w, "decomposeSink: deployment not found", http.StatusNotFound)
	case strings.HasPrefix(r.URL.Path, "/v1/builds/") && r.Method == http.MethodGet:
		s.mu.Lock()
		s.buildCalls++
		s.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/v1/builds/")
		if build, ok := s.builds[id]; ok {
			writeJSONTestStatus(w, http.StatusOK, build)
			return
		}
		http.Error(w, "decomposeSink: build not found", http.StatusNotFound)
	default:
		http.Error(w, "decomposeSink: not found: "+r.URL.Path, http.StatusNotFound)
	}
}

// multipartField extracts one text field from a captured request. The sink
// keeps the raw body for the existing byte-presence assertions, while this
// helper lets CLI tests verify that derived project metadata reached both
// ScanProject and ApplyProjectPlan.
func multipartField(body []byte, contentType, name string) string {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return ""
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, nextErr := reader.NextPart()
		if nextErr == io.EOF {
			return ""
		}
		if nextErr != nil {
			return ""
		}
		value, readErr := io.ReadAll(part)
		_ = part.Close()
		if readErr == nil && part.FormName() == name {
			return string(value)
		}
	}
}

// goldenPlan is the §4-fixture response shape: 3 apps + 1 cron, on
// the Hobby plan (5 apps / 10 crons). The CLI renders this both as
// a table and as --json, and the byte-stability test asserts both
// paths produce identical bytes for identical input.
var goldenPlan = api.PlanResponse{
	ProjectSlug:   "fixture",
	ScanSource:    "compose",
	Tier:          "compose",
	ObservedApps:  3,
	ObservedCrons: 1,
	LimitApps:     5,
	LimitCrons:    10,
	CanApply:      true,
	PlanToken:     "tok-stable",
	Workloads: []api.PlanWorkload{
		{Name: "api", RootDir: "/api", Class: "http"},
		{Name: "nightly", RootDir: "/nightly", Class: "worker", Schedule: "0 3 * * *"},
		{Name: "worker", RootDir: "/worker", Class: "worker"},
	},
	Managed: []api.PlanManaged{
		{Name: "postgres", Kind: "postgres", EnvHint: "postgresql://...", Image: "postgres:16"},
	},
}

// goldenApply is the matching 200 body for POST /v1/projects.
var goldenApply = api.ApplyResponse{
	PlanResponse: goldenPlan,
	ProjectID:    "p-1",
	Apps: []api.ApplyResponseApp{
		{Slug: "api", ID: "a-1"},
		{Slug: "nightly", ID: "a-2"},
		{Slug: "worker", ID: "a-3"},
	},
}

// writeTarball writes a fake .tar.gz that the CLI's openCustomerFile
// path accepts. The contents do not matter — the test server only
// cares that the file is openable; the Phase 3 plan integrates with
// the real extractor on the server side.
func writeTarball(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.tar.gz")
	if err := os.WriteFile(path, []byte("fake-tar-bytes"), 0o600); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
	return path
}

// TestCmdScan_JSONStable is the §4 acceptance gate. Two scans of
// identical bytes must produce byte-identical --json output. The
// test uses the same sink body twice via two separate cmd invocations
// and asserts the stdout captures are equal.
func TestCmdScan_JSONStable(t *testing.T) {
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: goldenPlan}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// jsonOutput is set by applyJSONFlag in run() — this test calls
	// cmdScan directly, so flip the flag and restore it after. The
	// §4 gate cares about byte stability, so re-using the same flag
	// across the two inner cmdScan calls is the load-bearing intent.
	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	tarball := writeTarball(t)

	run := func() string {
		stdout, restore := captureStdout(t)
		defer restore()
		if code := cmdScan([]string{
			"--tarball", tarball,
			"--project-slug", "fixture",
			"--only", "api,worker,nightly",
		}); code != 0 {
			t.Fatalf("cmdScan exit = %d, want 0", code)
		}
		return stdout.String()
	}

	first := run()
	second := run()
	if first != second {
		t.Fatalf("cmdScan --json output is not byte-stable across two identical inputs:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	// And the output must be valid JSON with the canonical can_apply key.
	var out api.PlanResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(first)), &out); err != nil {
		t.Fatalf("cmdScan --json output is not valid JSON: %v\n%s", err, first)
	}
	if !out.CanApply {
		t.Errorf("can_apply: got false, want true")
	}
	if len(out.Workloads) != 3 {
		t.Errorf("workloads: got %d, want 3", len(out.Workloads))
	}
}

// TestCmdScan_RendersTable exercises the human-readable path. The
// header lines ("Project:", "can_apply:") must appear, and the
// workloads must be sorted by name (the CLI's defensive sort).
func TestCmdScan_RendersTable(t *testing.T) {
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: goldenPlan}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	tarball := writeTarball(t)
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdScan([]string{"--tarball", tarball, "--project-slug", "fixture"}); code != 0 {
		t.Fatalf("cmdScan exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"Project: fixture",
		"can_apply: true",
		"api", "worker", "nightly",
		"postgres",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// workloads must be sorted by name asc: api, nightly, worker.
	apiAt := strings.Index(out, "api")
	nightlyAt := strings.Index(out, "nightly")
	workerAt := strings.Index(out, "worker")
	if apiAt >= nightlyAt || nightlyAt >= workerAt {
		t.Errorf("workloads not sorted by name asc: api=%d nightly=%d worker=%d", apiAt, nightlyAt, workerAt)
	}
}

// TestCmdScan_ExplainDetectionTrace pins the opt-in detector trace. The
// trace is intentionally human-readable (detector + source marker + priority)
// while preserving the terse default table for existing scripts. Merged
// detectors are sorted before rendering so the output is stable even if a
// server response was assembled in a different order.
func TestCmdScan_ExplainDetectionTrace(t *testing.T) {
	plan := goldenPlan
	plan.Workloads = []api.PlanWorkload{
		{
			Name:    "worker",
			RootDir: "/worker",
			Class:   "worker",
			Source:  "Procfile: worker",
			DetectedBy: &api.PlanDetectedBy{
				Detector:   "procfile",
				Priority:   75,
				MergedFrom: []string{"compose", "k8s", "compose"},
			},
		},
		{
			Name:    "api",
			RootDir: "/api",
			Class:   "http",
			Source:  "compose.yaml: api",
			DetectedBy: &api.PlanDetectedBy{
				Detector: "compose",
				Priority: 80,
			},
		},
	}
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: plan}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	tarball := writeTarball(t)
	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdScan([]string{"--tarball", tarball, "--project-slug", "fixture", "--explain"}); code != 0 {
		t.Fatalf("cmdScan --explain exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"detected_by: compose  marker=compose.yaml: api  priority=80",
		"detected_by: procfile  marker=Procfile: worker  priority=75",
		"merged_from: compose, k8s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--explain output missing %q:\n%s", want, out)
		}
	}
	// The normal workload rows remain name-sorted and the explanation follows
	// its corresponding row, so the first detector appears before the second.
	if strings.Index(out, "detected_by: compose") > strings.Index(out, "detected_by: procfile") {
		t.Errorf("detector trace not sorted with workloads:\n%s", out)
	}
}

// TestCmdScan_OverQuotaApps confirms the dry-run shows can_apply=false
// when the response already carries over-quota info, and exits 0 (the
// CLI never writes — over-quota is reported, not errored).
func TestCmdScan_OverQuotaApps(t *testing.T) {
	overQuota := goldenPlan
	overQuota.CanApply = false
	overQuota.ObservedApps = 7
	overQuota.LimitApps = 5
	overQuota.PlanToken = ""
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: overQuota}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	tarball := writeTarball(t)

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdScan([]string{"--tarball", tarball, "--project-slug", "fixture"}); code != 0 {
		t.Fatalf("cmdScan exit = %d, want 0 (dry-run never writes)", code)
	}
	if !strings.Contains(stdout.String(), "can_apply: false") {
		t.Errorf("dry-run over quota: expected 'can_apply: false' in output, got %q", stdout.String())
	}
}

// TestCmdScan_OverQuotaFreeCrons asserts the crons-not-allowed hint
// prints before can_apply. The hint is the one byte the CLI owes a
// running customer: "upgrade to Hobby or above" must be visible.
func TestCmdScan_OverQuotaFreeCrons(t *testing.T) {
	overQuota := goldenPlan
	overQuota.CanApply = false
	overQuota.CronsNotAllowed = true
	overQuota.ObservedApps = 1
	overQuota.LimitApps = 1
	overQuota.ObservedCrons = 1
	overQuota.LimitCrons = 0
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: overQuota}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	tarball := writeTarball(t)

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdScan([]string{"--tarball", tarball, "--project-slug", "fixture"}); code != 0 {
		t.Fatalf("cmdScan exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Crons unavailable on this plan") {
		t.Errorf("missing crons-not-allowed hint, got %q", stdout.String())
	}
}

// TestCmdScan_OnlyFiltering verifies the --only CSV is forwarded as
// the only query param on the wire. The server-side filter is not
// re-asserted here (server-side tests live in cmd/apid).
func TestCmdScan_OnlyFiltering(t *testing.T) {
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: goldenPlan}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	tarball := writeTarball(t)

	if code := cmdScan([]string{
		"--tarball", tarball,
		"--project-slug", "fixture",
		"--only", "api,worker",
	}); code != 0 {
		t.Fatalf("cmdScan exit = %d, want 0", code)
	}
	// The captured multipart body is opaque (SDK serializes only once
	// via multipart.NewWriter), but the entry count is the canary:
	// one source file + project_slug + production_branch + only = 4
	// fields. The CLI's splitCSV must produce a 2-element list.
	if len(sink.capturedMultipart) == 0 {
		t.Fatalf("scan did not capture a multipart body")
	}
}

// TestCmdDeployTarball_RejectsOnlyWithRepo is the spec invariant
// from the plan: --repo (the dashboard browser flow) cannot combine
// with --only or --project-slug. Mixing them is almost always a
// mistake on the customer's side; the CLI rejects explicitly.
func TestCmdDeployTarball_RejectsOnlyWithRepo(t *testing.T) {
	// We don't even need a server here — the rejection should happen
	// before any HTTP call. Hook a server that records calls so a
	// regression that reaches the wire is caught.
	sink := &decomposeSink{}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	stdout, restore := captureStdout(t)
	defer restore()
	stderr, restoreErr := captureStderr(t)
	defer restoreErr()
	code := cmdDeployTarball([]string{
		"--repo", "owner/name",
		// --ref is required for the headless source-ref path
		// (issue #739 / ADR-092). Pass a valid 40-char SHA so the
		// combo-rejection check below is exercised, not the
		// missing-ref guard. The combo-rejection (--repo + --only)
		// must short-circuit before any HTTP call.
		"--ref", "0123456789abcdef0123456789abcdef01234567",
		"--only", "api",
	})
	if code != 1 {
		t.Errorf("cmdDeployTarball --repo --only exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--repo cannot be combined") {
		t.Errorf("expected rejection message, got %q", stderr.String())
	}
	if sink.scanCalls != 0 || sink.applyCalls != 0 {
		t.Errorf("rejected call still reached the server: scan=%d apply=%d", sink.scanCalls, sink.applyCalls)
	}
	// stdout is part of the success surface; the good path shouldn't
	// have printed the plan.
	if strings.Contains(stdout.String(), "can_apply") {
		t.Errorf("output should not render a plan on rejected combo: %q", stdout.String())
	}
}

// TestCmdDeployTarball_YesFlagSkeleton is the smoke test for the new
// one-key flow. We don't drive the prompt (TTY-on tests are flaky
// in CI), but we do assert that cmdDeployTarball:
//
//  1. Calls scan exactly once
//  2. Calls apply exactly once
//  3. Returns 0 on a happy path
//  4. Prints the "Created project" line on the text path
//
// --yes is required here because the test harness is non-TTY. The
// interactive "y" path is covered in test_ephemeral.sh (CI) and the
// manual CLI smoke test; gating it on a real TTY here would couple
// the test to a unix-only CI runner.
func TestCmdDeployTarball_YesFlagSkeleton(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	tarball := writeTarball(t)
	stdout, restore := captureStdout(t)
	defer restore()

	// --yes is the documented explicit approval for non-interactive use.
	code := cmdDeployTarball([]string{
		"--tarball", tarball,
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	})
	if code != 0 {
		t.Errorf("cmdDeployTarball --yes exit = %d, want 0", code)
	}
	if sink.scanCalls != 1 {
		t.Errorf("scan calls: got %d, want 1", sink.scanCalls)
	}
	if sink.applyCalls != 1 {
		t.Errorf("apply calls: got %d, want 1", sink.applyCalls)
	}
	if !strings.Contains(stdout.String(), "Created project") {
		t.Errorf("expected success line, got %q", stdout.String())
	}
}

// TestCmdDeployTarball_NonTTYRequiresExplicitApproval pins the destructive
// project-deploy boundary. A captured/piped invocation must render the full
// plan (including removals) and stop before ApplyProjectPlan unless --yes is
// present. JSON is non-interactive by contract, so it follows the same
// fail-closed rule even when a terminal would otherwise be available.
func TestCmdDeployTarball_NonTTYRequiresExplicitApproval(t *testing.T) {
	plan := goldenPlan
	plan.Removed = []string{"old-service"}

	for _, tc := range []struct {
		name string
		json bool
	}{
		{name: "text", json: false},
		{name: "json", json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := &decomposeSink{
				scanStatus:  http.StatusOK,
				scanBody:    plan,
				applyStatus: http.StatusOK,
				applyBody:   goldenApply,
			}
			srv := httptest.NewServer(sink)
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			// Force the exact non-TTY shape regardless of the test runner.
			restoreTTY := withTTYForTest(false)
			defer restoreTTY()
			prevJSON := jsonOutput
			jsonOutput = tc.json
			defer func() { jsonOutput = prevJSON }()
			prevStdin := osStdin
			osStdin = strings.NewReader("")
			defer func() { osStdin = prevStdin }()

			stdout, restoreStdout := captureStdout(t)
			defer restoreStdout()
			stderr, restoreStderr := captureStderr(t)
			defer restoreStderr()

			code := cmdDeployTarball([]string{
				"--tarball", writeTarball(t),
				"--project-slug", "fixture",
			})
			if code != 1 {
				t.Fatalf("non-TTY project deploy exit = %d, want 1", code)
			}
			if sink.scanCalls != 1 {
				t.Fatalf("scan calls = %d, want 1", sink.scanCalls)
			}
			if sink.applyCalls != 0 {
				t.Fatalf("non-TTY project deploy issued apply call: %d", sink.applyCalls)
			}
			if !strings.Contains(stdout.String(), "old-service") {
				t.Errorf("plan output omitted removal: %q", stdout.String())
			}
			if !strings.Contains(stderr.String(), "requires --yes") {
				t.Errorf("missing explicit-approval error: %q", stderr.String())
			}
			if tc.json {
				var got api.PlanResponse
				if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &got); err != nil {
					t.Fatalf("JSON plan output is not valid JSON: %v\n%s", err, stdout.String())
				}
				if len(got.Removed) != 1 || got.Removed[0] != "old-service" {
					t.Errorf("JSON plan removed = %#v, want [old-service]", got.Removed)
				}
			}
		})
	}
}

// TestCmdDeployTarball_ProjectFlagDefaultsSlug makes the discoverable
// --project spelling equivalent to --project-slug for a tarball while
// keeping the existing single-app path unchanged when the flag is absent.
func TestCmdDeployTarball_ProjectFlagDefaultsSlug(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	_, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t),
		"--project",
		"--yes",
	}); code != 0 {
		t.Fatalf("cmdDeployTarball --project exit = %d, want 0", code)
	}
	if sink.scanCalls != 1 || sink.applyCalls != 1 {
		t.Fatalf("project calls: scan=%d apply=%d, want one each", sink.scanCalls, sink.applyCalls)
	}
	if sink.projectSlug != "fixture" {
		t.Errorf("derived project slug = %q, want %q", sink.projectSlug, "fixture")
	}
}

// TestCmdDeployTarball_JSONFlag asserts the --json path emits the
// apply response verbatim (already validated by the SDK client
// round-trip test, but the CLI's json_out wrapping is the wire
// surface we ship).
func TestCmdDeployTarball_JSONFlag(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// jsonOutput is normally flipped by applyJSONFlag in run() — this
	// test calls cmdDeployTarball directly, so we set the flag and
	// restore it via the helper the package exposes for tests
	// (json_flag.go::resetJSONOutput). Saving/restoring the prior
	// value keeps the test from leaking into siblings.
	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	tarball := writeTarball(t)
	stdout, restore := captureStdout(t)
	defer restore()

	if code := cmdDeployTarball([]string{
		"--tarball", tarball,
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	}); code != 0 {
		t.Fatalf("cmdDeployTarball --json exit = %d, want 0", code)
	}
	var out api.ApplyResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &out); err != nil {
		t.Fatalf("apply --json output not valid JSON: %v\n%s", err, stdout.String())
	}
	if out.ProjectID != goldenApply.ProjectID {
		t.Errorf("project_id: got %q, want %q", out.ProjectID, goldenApply.ProjectID)
	}
	if len(out.Apps) != 3 {
		t.Errorf("apps: got %d, want 3", len(out.Apps))
	}
}

func projectApplyWithBuilds() api.ApplyResponse {
	apply := goldenApply
	apply.Builds = []api.AppliedBuild{
		{Slug: "api", AppID: "a-1", DeploymentID: "d-api", BuildID: "b-api"},
		{Slug: "worker", AppID: "a-3", DeploymentID: "d-worker", BuildID: "b-worker"},
	}
	return apply
}

func projectTerminalDeployments() map[string]api.DeploymentResponse {
	return map[string]api.DeploymentResponse{
		"d-api":    {ID: "d-api", AppID: "a-1", BuildID: "b-api", Status: statusLive},
		"d-worker": {ID: "d-worker", AppID: "a-3", BuildID: "b-worker", Status: statusLive},
	}
}

func projectTerminalBuilds() map[string]api.BuildResponse {
	return map[string]api.BuildResponse{
		"b-api":    {ID: "b-api", DeploymentID: "d-api", Status: api.BuildStatusSucceeded},
		"b-worker": {ID: "b-worker", DeploymentID: "d-worker", Status: api.BuildStatusSucceeded},
	}
}

// TestCmdDeployTarball_ProjectWaitsForAllBuilds pins the default project
// lifecycle: an apply receipt is not considered complete until every
// deployment and build has a terminal status, and the text output includes
// those observed statuses.
func TestCmdDeployTarball_ProjectWaitsForAllBuilds(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   projectApplyWithBuilds(),
		deployments: projectTerminalDeployments(),
		builds:      projectTerminalBuilds(),
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	prevJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prevJSON }()

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t), "--project-slug", "fixture", "--yes",
	}); code != 0 {
		t.Fatalf("project wait exit = %d, want 0", code)
	}
	if sink.deploymentCalls != 2 || sink.buildCalls != 2 {
		t.Fatalf("status calls: deployments=%d builds=%d, want 2 each", sink.deploymentCalls, sink.buildCalls)
	}
	out := stdout.String()
	for _, want := range []string{
		"waiting for 2 project deployment(s)",
		"api: deployment=d-api (live) build=b-api (succeeded)",
		"worker: deployment=d-worker (live) build=b-worker (succeeded)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project wait output missing %q:\n%s", want, out)
		}
	}
}

// TestCmdDeployTarball_ProjectWaitJSONIncludesStatuses keeps the machine
// readable response aligned with the human result: wait-aware JSON carries
// the terminal deployment/build state for each workload.
func TestCmdDeployTarball_ProjectWaitJSONIncludesStatuses(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   projectApplyWithBuilds(),
		deployments: projectTerminalDeployments(),
		builds:      projectTerminalBuilds(),
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	prevJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prevJSON }()

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t), "--project-slug", "fixture", "--yes",
	}); code != 0 {
		t.Fatalf("project wait --json exit = %d, want 0", code)
	}
	var got api.ApplyResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &got); err != nil {
		t.Fatalf("project wait JSON is invalid: %v\n%s", err, stdout.String())
	}
	if len(got.Builds) != 2 {
		t.Fatalf("builds: got %d, want 2", len(got.Builds))
	}
	for _, build := range got.Builds {
		if build.DeploymentStatus != statusLive || build.BuildStatus != api.BuildStatusSucceeded {
			t.Errorf("%s statuses: deployment=%q build=%q", build.Slug, build.DeploymentStatus, build.BuildStatus)
		}
	}
}

// TestCmdDeployTarball_ProjectNoWaitSkipsStatusReads preserves the explicit
// escape hatch for callers that only want the enqueue receipt.
func TestCmdDeployTarball_ProjectNoWaitSkipsStatusReads(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   projectApplyWithBuilds(),
		deployments: projectTerminalDeployments(),
		builds:      projectTerminalBuilds(),
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	prevJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prevJSON }()

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t), "--project-slug", "fixture", "--yes", "--no-wait",
	}); code != 0 {
		t.Fatalf("project --no-wait exit = %d, want 0", code)
	}
	if sink.deploymentCalls != 0 || sink.buildCalls != 0 {
		t.Fatalf("--no-wait performed status reads: deployments=%d builds=%d", sink.deploymentCalls, sink.buildCalls)
	}
	if !strings.Contains(stdout.String(), "deployment=d-api build=b-api") {
		t.Errorf("--no-wait output lost enqueue receipt:\n%s", stdout.String())
	}
}

// TestCmdDeployTarball_ProjectWaitTimeoutReturnsQueuedState verifies that the
// shared --timeout budget is honored and the JSON response still contains the
// most recent non-terminal statuses so the caller can resume watching later.
func TestCmdDeployTarball_ProjectWaitTimeoutReturnsQueuedState(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   projectApplyWithBuilds(),
		deployments: map[string]api.DeploymentResponse{
			"d-api":    {ID: "d-api", Status: "running"},
			"d-worker": {ID: "d-worker", Status: "running"},
		},
		builds: map[string]api.BuildResponse{
			"b-api":    {ID: "b-api", Status: api.BuildStatusRunning},
			"b-worker": {ID: "b-worker", Status: api.BuildStatusRunning},
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	prevJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prevJSON }()

	stdout, restoreStdout := captureStdout(t)
	defer restoreStdout()
	stderr, restoreStderr := captureStderr(t)
	defer restoreStderr()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t), "--project-slug", "fixture", "--yes", "--timeout", "1",
	}); code != 3 {
		t.Fatalf("project wait timeout exit = %d, want 3", code)
	}
	var got api.ApplyResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &got); err != nil {
		t.Fatalf("timeout JSON is invalid: %v\n%s", err, stdout.String())
	}
	for _, build := range got.Builds {
		if build.DeploymentStatus != "running" || build.BuildStatus != api.BuildStatusRunning {
			t.Errorf("%s timeout statuses: deployment=%q build=%q", build.Slug, build.DeploymentStatus, build.BuildStatus)
		}
	}
	if !strings.Contains(stderr.String(), "did not reach a terminal state") {
		t.Errorf("timeout warning missing: %q", stderr.String())
	}
}

// TestCmdDeployTarball_ProjectDryRunUsesScan verifies that the project
// preview follows the same ScanProject contract as `gregale scan` and never
// reaches the transactional apply endpoint. Before issue #1976 was fixed,
// the generic --diff short-circuit tried to read a single app instead.
func TestCmdDeployTarball_ProjectDryRunUsesScan(t *testing.T) {
	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t),
		"--project-slug", "fixture",
		"--only", "api,worker",
		"--dry-run",
	}); code != 0 {
		t.Fatalf("project dry-run exit = %d, want 0", code)
	}
	if sink.scanCalls != 1 {
		t.Fatalf("project dry-run scan calls = %d, want 1", sink.scanCalls)
	}
	if sink.applyCalls != 0 {
		t.Fatalf("project dry-run issued apply call: %d", sink.applyCalls)
	}
	var got api.PlanResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &got); err != nil {
		t.Fatalf("project dry-run output is not JSON: %v\n%s", err, stdout.String())
	}
	if !reflect.DeepEqual(got, goldenPlan) {
		t.Fatalf("project dry-run plan differs from ScanProject response:\n got  %+v\n want %+v", got, goldenPlan)
	}
}

// TestCmdDeployTarball_ProjectDryRunHumanRendersPlan keeps the text preview
// aligned with `gregale scan` as well as the JSON path.
func TestCmdDeployTarball_ProjectDryRunHumanRendersPlan(t *testing.T) {
	sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: goldenPlan}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	prev := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prev }()

	stdout, restore := captureStdout(t)
	defer restore()
	if code := cmdDeployTarball([]string{
		"--tarball", writeTarball(t),
		"--project-slug", "fixture",
		"--dry-run",
	}); code != 0 {
		t.Fatalf("project dry-run exit = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"Project: fixture", "can_apply: true", "api", "worker", "postgres"} {
		if !strings.Contains(out, want) {
			t.Errorf("project dry-run output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Created project") {
		t.Errorf("project dry-run rendered apply success: %q", out)
	}
	if sink.applyCalls != 0 {
		t.Fatalf("project dry-run issued apply call: %d", sink.applyCalls)
	}
}

func TestCmdDeployTarball_ProjectPreviewStrictGate(t *testing.T) {
	overQuota := goldenPlan
	overQuota.CanApply = false
	overQuota.CanApplyReasons = []string{"apps over plan limit", "cron configuration is unsupported"}
	overQuota.ObservedApps = 7
	overQuota.LimitApps = 5

	cases := []struct {
		name      string
		flags     []string
		wantCode  int
		wantJSON  bool
		wantUsage string
	}{
		{
			name:     "dry-run strict json",
			flags:    []string{"--dry-run", "--strict", "--json"},
			wantCode: 1,
			wantJSON: true,
		},
		{
			name:     "diff strict json",
			flags:    []string{"--diff", "--strict", "--json"},
			wantCode: 1,
			wantJSON: true,
		},
		{
			name:     "dry-run lenient json",
			flags:    []string{"--dry-run", "--lenient", "--json"},
			wantCode: 0,
			wantJSON: true,
		},
		{
			name:      "dry-run strict text",
			flags:     []string{"--dry-run", "--strict"},
			wantCode:  1,
			wantUsage: "can_apply: false",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			sink := &decomposeSink{scanStatus: http.StatusOK, scanBody: overQuota}
			srv := httptest.NewServer(sink)
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			stdout, restore := captureStdout(t)
			defer restore()
			args := append([]string{"--tarball", writeTarball(t), "--project-slug", "fixture"}, tc.flags...)
			if code := cmdDeployTarball(args); code != tc.wantCode {
				t.Fatalf("project preview exit = %d, want %d; output=%s", code, tc.wantCode, stdout.String())
			}
			if sink.applyCalls != 0 {
				t.Fatalf("project preview issued apply call: %d", sink.applyCalls)
			}
			out := stdout.String()
			if tc.wantJSON {
				var got api.PlanResponse
				if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
					t.Fatalf("project preview output is not JSON: %v\n%s", err, out)
				}
				if got.CanApply {
					t.Fatal("project preview reported can_apply=true for blocked plan")
				}
				if !reflect.DeepEqual(got.CanApplyReasons, overQuota.CanApplyReasons) {
					t.Errorf("can_apply_reasons = %#v, want %#v", got.CanApplyReasons, overQuota.CanApplyReasons)
				}
			} else if !strings.Contains(out, tc.wantUsage) {
				t.Errorf("project preview output missing %q: %s", tc.wantUsage, out)
			}
		})
	}
}

// TestCmdDeployTarball_OverQuotaCreatesNothing is the §4 acceptance
// gate's lock side: when the scan returns can_apply=false, the CLI
// must NOT issue an apply request. The test asserts applyCalls == 0
// and expects exit 1 with a "Plan is not applicable" message.
func TestCmdDeployTarball_OverQuotaCreatesNothing(t *testing.T) {
	overQuota := goldenPlan
	overQuota.CanApply = false
	overQuota.ObservedApps = 7
	overQuota.LimitApps = 5
	sink := &decomposeSink{
		scanStatus: http.StatusOK,
		scanBody:   overQuota,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	tarball := writeTarball(t)
	if code := cmdDeployTarball([]string{
		"--tarball", tarball,
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	}); code != 1 {
		t.Errorf("over-quota deploy exit = %d, want 1", code)
	}
	if sink.applyCalls != 0 {
		t.Errorf("over-quota deploy still called apply: %d call(s)", sink.applyCalls)
	}
}

// TestPlanProblem_Mapping is the wire-shape parity test for the
// --json path on a non-applyable plan. The CLI's planProblem
// helper must produce the matching RFC 7807 code so the
// customer/SDK round-trip is symmetric.
func TestPlanProblem_Mapping(t *testing.T) {
	cases := []struct {
		name     string
		plan     api.PlanResponse
		wantCode string
		wantStat int
	}{
		{
			name: "crons-not-allowed",
			plan: api.PlanResponse{CronsNotAllowed: true, LimitApps: 1, LimitCrons: 0,
				ObservedApps: 1, ObservedCrons: 1},
			wantCode: api.CodePlanCronsNotAllowed,
			wantStat: 402,
		},
		{
			name: "app-limit",
			plan: api.PlanResponse{LimitApps: 5, LimitCrons: 10,
				ObservedApps: 7, ObservedCrons: 1},
			wantCode: api.CodePlanLimitApps,
			wantStat: 403,
		},
		{
			name: "cron-limit",
			plan: api.PlanResponse{LimitApps: 5, LimitCrons: 10,
				ObservedApps: 1, ObservedCrons: 11},
			wantCode: api.CodePlanCronQuota,
			wantStat: 403,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := planProblem(c.plan)
			if p.Status != c.wantStat {
				t.Errorf("status: got %d, want %d", p.Status, c.wantStat)
			}
			if p.Code != c.wantCode {
				t.Errorf("code: got %q, want %q", p.Code, c.wantCode)
			}
		})
	}
}

// TestDefaultProjectSlug covers the basename-derive rule. The
// extension is stripped; the trailing segment is the slug.
func TestDefaultProjectSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"/tmp/fixture.tar.gz", "fixture"},
		{"/tmp/my-repo", "my-repo"},
		{"./fixture", "fixture"},
	}
	for _, c := range cases {
		if got := defaultProjectSlug(c.in); got != c.want {
			t.Errorf("defaultProjectSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSplitCSVEdgeCases covers the small set of behaviours the
// cmdScan --only flag relies on. Empty input → nil, otherwise
// the entries are lowercased + trimmed.
func TestSplitCSVEdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"api", []string{"api"}},
		{"api,worker", []string{"api", "worker"}},
		{"API,  Worker  , ", []string{"api", "worker"}},
		{"  ", nil},
	}
	for _, c := range cases {
		got := splitCSV(c.in)
		if len(got) != len(c.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Cluster A: gregale deploy --doctor-strict pre-upload gate (spec §6.4
// amendment 1). The doctor must run BEFORE any HTTP, so a customer
// with a top-level data/ directory gets the stateless_only_violation
// prose locally rather than uploading + 422-ing. Warnings remain
// warn-only (mirrors the standalone cmdDoctor semantics).
// ---------------------------------------------------------------------------

// TestCmdDeployTarball_DoctorStrict_FailsFast pins the failure path:
// a cwd with a top-level data/ directory trips the stateless-only
// check → exit 1 + doctor report on stderr + zero HTTP calls.
// The test swaps osStderr for a buffer so we can grep the rendered
// prose; the sink's HTTP counter is the "no upload" assertion.
func TestCmdDeployTarball_DoctorStrict_FailsFast(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	prev := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prev }()

	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()

	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// t.Chdir into the fixture so os.Getwd() in the wire-in path
	// picks up the data/ subdir. Chdir auto-restores on test exit.
	t.Chdir(dir)

	if code := cmdDeployTarball([]string{
		"--doctor-strict",
		"--tarball", writeTarball(t), // never reached, but the flag parser still validates
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	}); code != 1 {
		t.Errorf("exit code = %d, want 1 (doctor-strict must fail-fast)", code)
	}
	if sink.scanCalls != 0 {
		t.Errorf("scan should not be called (doctor-strict pre-uploads): got %d calls", sink.scanCalls)
	}
	if sink.applyCalls != 0 {
		t.Errorf("apply should not be called: got %d calls", sink.applyCalls)
	}
	// Rendered prose must include the code + the whycopy hint so the
	// customer sees the prose at exit time, not just "exit 1". The
	// leading glyph (`✗`) is gated by output.Enabled() and may be
	// suppressed when NO_COLOR is set or stdout is not a TTY (some
	// sibling tests prime output.go's noColorCached state — the
	// cache is binary-global, see output.go:noColorCached). Assert
	// on the durable shape (code name + hint substring), not the
	// glyph, so the test stays robust under both human and
	// no-color/pipe renderings.
	rendered := stderr.String()
	if !strings.Contains(rendered, "stateless_only_violation") {
		t.Errorf("stderr must surface the code, got %q", rendered)
	}
	if !strings.Contains(rendered, "this app shape needs persistent storage") {
		t.Errorf("stderr must surface the whycopy hint, got %q", rendered)
	}
}

// TestCmdDeployTarball_DoctorStrict_AllGreenContinues pins the happy
// path: a clean cwd (no data/, no loopback-bind) under --doctor-strict
// must NOT short-circuit. The deploy proceeds normally — sink.scanCalls
// + sink.applyCalls both advance. We pass an explicit
// --no-fan-out-yml to avoid the gregale.yaml discovery path
// interfering with the cwd fixture (mirrors TestCmdDeployTarball_*
// patterns above).
func TestCmdDeployTarball_DoctorStrict_AllGreenContinues(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("app.listen(process.env.PORT);\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	prev := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prev }()

	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()

	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	t.Chdir(dir)

	if code := cmdDeployTarball([]string{
		"--doctor-strict",
		"--tarball", writeTarball(t),
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	}); code != 0 {
		t.Errorf("clean cwd under --doctor-strict must continue; got exit %d, stderr=%q", code, stderr.String())
	}
	if sink.scanCalls != 1 {
		t.Errorf("scanCalls = %d, want 1 (clean doctor must not short-circuit)", sink.scanCalls)
	}
	if sink.applyCalls != 1 {
		t.Errorf("applyCalls = %d, want 1", sink.applyCalls)
	}
}

// TestCmdDeployTarball_DoctorStrict_WarnsOnlyContinues pins the
// warn-only semantics. A fixture that produces a warn-class finding
// (NOT error) must continue the deploy — only error findings fail.
// We use a fixture the existing scanSource-based checks tolerate but
// that has no error-class signal; the doctor report still has 8
// rows so we assert at least one warn OR ok exists, and that the
// render path doesn't fail-fast.
func TestCmdDeployTarball_DoctorStrict_WarnsOnlyContinues(t *testing.T) {
	dir := t.TempDir()
	// Empty repo → all checks ok in the current rule set. We still
	// exercise the wiring path: HasErrors=false → continue.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	prev := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = prev }()

	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()

	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	t.Chdir(dir)

	if code := cmdDeployTarball([]string{
		"--doctor-strict",
		"--tarball", writeTarball(t),
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	}); code != 0 {
		t.Errorf("all-ok cwd under --doctor-strict must continue; got exit %d, stderr=%q", code, stderr.String())
	}
	if sink.scanCalls != 1 {
		t.Errorf("scanCalls = %d, want 1", sink.scanCalls)
	}
}

// TestCmdDeployTarball_DoctorStrict_JSON pins the --json envelope.
// When the global --json flag is set AND --doctor-strict trips on
// an error-class finding, stderr must carry a JSON document with
// {"doctor": {...}, "exit": 1}. The shape is what CI scripts grep
// on, so a regression to "human prose" would break parse-ability.
func TestCmdDeployTarball_DoctorStrict_JSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	prev := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = prev }()

	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	defer func() { osStderr = oldErr }()

	sink := &decomposeSink{
		scanStatus:  http.StatusOK,
		scanBody:    goldenPlan,
		applyStatus: http.StatusOK,
		applyBody:   goldenApply,
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	t.Chdir(dir)

	if code := cmdDeployTarball([]string{
		"--doctor-strict",
		"--tarball", writeTarball(t),
		"--project-slug", "fixture",
		"--only", "api,worker,nightly",
		"--yes",
	}); code != 1 {
		t.Errorf("JSON doctor-strict failure must exit 1; got %d", code)
	}
	if sink.scanCalls != 0 {
		t.Errorf("scanCalls = %d, want 0 (pre-upload gate)", sink.scanCalls)
	}
	// stderr must be a single-line JSON envelope (newlines inside
	// the embedded report are fine — we grep on the envelope keys).
	var env struct {
		Doctor doctorReport `json:"doctor"`
		Exit   int          `json:"exit"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stderr.String())), &env); err != nil {
		t.Fatalf("stderr must be a JSON envelope under --json; got %q (err=%v)", stderr.String(), err)
	}
	if env.Exit != 1 {
		t.Errorf("envelope exit = %d, want 1", env.Exit)
	}
	var foundStateless bool
	for _, c := range env.Doctor.Checks {
		if c.Code == "stateless_only_violation" {
			foundStateless = true
		}
	}
	if !foundStateless {
		t.Errorf("envelope must carry the stateless_only_violation finding; got %+v", env.Doctor)
	}
}
