// cmd_deploy_source_ref_test.go — whitebox CLI tests for the headless
// source-ref deploy path (issue #739 / DEPLOY-PROV-4 / ADR-092).
//
// Drives cmdDeployTarball (the --repo --ref branch dispatch) and
// cmdDeployRepoSourceRef directly. The httptest sink mirrors the
// PR-A server contract:
//   - POST /v1/apps/{slug}/deployments/source-ref
//   - 202 Accepted on success
//   - 409 + Retry-After on transient githubd / codeload blips
//   - 404 + code=github_install_not_found when the durable install
//     row is absent
//
// All sub-cases are table-driven under TestCmdDeployRepoSourceRef so
// the package-level test binary incurs the httptest + captureStdout
// + captureStderr + env setup cost once (the same shape as
// pgstore-coverage-sweep-timeout-fix: 41 TestPg_ → 3 top-level +
// sub-tests; sub-tests share the parent's fixture). The
// "no install token env" sub-case is the load-bearing CI regression
// test for PR-A's whole point: a runner with only FAAS_TOKEN set
// can drive a deploy end-to-end, no GREGALE_INSTALL_TOKEN_* needed.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// sourceRefSink is the http test server for the source-ref deploy
// surface. Mirrors the shape of decomposeSink (commands_decompose_test.go:37)
// but for /v1/apps/{slug}/deployments/source-ref. Public fields so
// tests can populate per-sub-case inline.
type sourceRefSink struct {
	// status + body mirror what the server emits. retryAfter is the
	// wire header (non-empty → server sets Retry-After: <value>).
	status     int
	body       api.DeploymentResponse
	problem    *api.Problem
	retryAfter string

	// captured records the wire request so assertions can pin
	// method, path, body shape, and the auto-minted Idempotency-Key.
	captured      *http.Request
	capturedBody  []byte
	capturedCalls int
}

// ServeHTTP is the single dispatch arm — the sink only knows the
// source-ref path. Any other path returns 404 with a useful message
// so a regression that drives the wrong wire URL fails loud.
func (s *sourceRefSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.captured = r
	body, _ := io.ReadAll(r.Body)
	s.capturedBody = body
	s.capturedCalls++
	if s.problem != nil {
		if s.retryAfter != "" {
			w.Header().Set("Retry-After", s.retryAfter)
		}
		writeJSONTestStatus(w, s.status, s.problem)
		return
	}
	writeJSONTestStatus(w, s.status, s.body)
}

// withResetJSONOutput flips the global jsonOutput flag for the
// duration of t and restores it. Mirrors the pattern used in
// commands_decompose_test.go for --json-mode tests.
func withResetJSONOutput(t *testing.T, v bool) {
	t.Helper()
	prev := jsonOutput
	jsonOutput = v
	t.Cleanup(func() { jsonOutput = prev })
}

// TestCmdDeployRepoSourceRef is the §4-style acceptance gate for
// issue #739. Table-driven so all sub-cases share the parent's
// httptest + env + capture setup cost (pg-shard-2 timeout edge
// per pgstore-coverage-sweep-timeout-fix).
//
// Sub-cases:
//   - happy_path: end-to-end POST + auto-minted Idempotency-Key +
//     JSON stdout.
//   - missing_ref: --repo without --ref exits 1 before any HTTP call.
//   - invalid_slug: validateRepoSlug guard.
//   - retry_after: 409 + Retry-After:30 → stderr surfaces
//     "(Retry-After: 30s)".
//   - not_found: 404 + code=github_install_not_found → stderr
//     surfaces the bind hint.
//   - no_install_token_env: §4 CI regression — happy path with
//     every GREGALE_INSTALL_TOKEN_* unset, asserts
//     Content-Type=application/json (JSON POST, not multipart).
func TestCmdDeployRepoSourceRef(t *testing.T) {
	// Issue #739 / ADR-092: regression — the CLI must NOT touch
	// any install-token env var. Unset every GREGALE_INSTALL_TOKEN_*
	// prefix once at the parent so all sub-cases run in the
	// canonical CI shape (no install-token env).
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "GREGALE_INSTALL_TOKEN_") {
			name := strings.SplitN(e, "=", 2)[0]
			t.Setenv(name, "")
		}
	}

	type expect struct {
		exitCode       int
		stderrContains []string
		// stdoutJSONKey is the DeploymentResponse.ID the CLI is
		// expected to print on stdout in --json mode. Empty =
		// don't assert stdout.
		stdoutJSONID string
		// wirePin runs when the test reaches the server; non-nil
		// means the sub-case should have produced an HTTP call.
		wirePin func(t *testing.T, sink *sourceRefSink)
	}

	cases := []struct {
		name string
		// setup mutates the sink in place.
		setup func(sink *sourceRefSink)
		// invoke is the CLI call under test. slug/repo/ref match
		// the sourceRefDeployRequest wire shape. ann is the
		// issue #977 / ADR-116 annotation bag — pre-feature
		// (zero-value) tests pass api.DeployAnnotations{} so
		// the legacy wire assertions stay valid.
		invoke func(slug, repo, ref string, ann api.DeployAnnotations) int
		expect expect
	}{
		{
			name: "happy_path",
			setup: func(sink *sourceRefSink) {
				sink.status = http.StatusAccepted
				sink.body = api.DeploymentResponse{
					ID: "dep_2", AppID: "app_hello", BuildID: "build_2",
					Kind: "github", Status: "queued",
				}
			},
			invoke: cmdDeployRepoSourceRef,
			expect: expect{
				exitCode:       0,
				stdoutJSONID:   "dep_2",
				stderrContains: nil,
				wirePin: func(t *testing.T, sink *sourceRefSink) {
					if sink.captured.Method != http.MethodPost {
						t.Errorf("method = %s, want POST", sink.captured.Method)
					}
					if sink.captured.URL.Path != "/v1/apps/hello/deployments/source-ref" {
						t.Errorf("path = %q, want /v1/apps/hello/deployments/source-ref", sink.captured.URL.Path)
					}
					if got := sink.captured.Header.Get("Idempotency-Key"); got == "" {
						t.Errorf("Idempotency-Key missing — Client.do should auto-mint for non-GET/HEAD")
					}
					var got api.SourceRefDeployRequest
					if err := json.Unmarshal(sink.capturedBody, &got); err != nil {
						t.Fatalf("decode request body: %v\n%s", err, sink.capturedBody)
					}
					if got.Repo != "onebox-faas/hello" {
						t.Errorf("body.repo = %q, want onebox-faas/hello", got.Repo)
					}
					if got.Ref != "0123456789abcdef0123456789abcdef01234567" {
						t.Errorf("body.ref = %q, want pinned SHA", got.Ref)
					}
					if got.Format != "tarball" {
						t.Errorf("body.format = %q, want tarball (PR-A only supports tarball)", got.Format)
					}
				},
			},
		},
		{
			name: "retry_after_409",
			setup: func(sink *sourceRefSink) {
				sink.status = http.StatusConflict
				sink.retryAfter = "30"
				sink.problem = &api.Problem{
					Status: http.StatusConflict,
					Code:   "source_ref_unavailable",
					Title:  "Source ref unavailable",
					Detail: "githubd stream timed out; retry after the indicated backoff.",
				}
			},
			invoke: cmdDeployRepoSourceRef,
			expect: expect{
				exitCode: 1, // non-zero
				stderrContains: []string{
					"Retry-After: 30s",
					"source_ref_unavailable",
				},
			},
		},
		{
			name: "not_found_404",
			setup: func(sink *sourceRefSink) {
				sink.status = http.StatusNotFound
				sink.problem = &api.Problem{
					Status: http.StatusNotFound,
					Code:   "github_install_not_found",
					Title:  "GitHub install not found",
					Detail: "no github_installations row for this account; run `gregale connect`",
				}
			},
			invoke: cmdDeployRepoSourceRef,
			expect: expect{
				exitCode: 1,
				stderrContains: []string{
					"github_install_not_found",
					"gregale connect",
				},
			},
		},
		{
			name:  "no_install_token_env_regression",
			setup: func(sink *sourceRefSink) {}, // already stripped at parent
			invoke: func(slug, repo, ref string, ann api.DeployAnnotations) int {
				return cmdDeployRepoSourceRef(slug, repo, ref, ann)
			},
			expect: expect{
				exitCode: 0,
				wirePin: func(t *testing.T, sink *sourceRefSink) {
					if got := sink.captured.Header.Get("Content-Type"); got != "application/json" {
						t.Errorf("Content-Type = %q, want application/json (source-ref is JSON, not multipart)", got)
					}
				},
			},
		},
	}

	const (
		slug = "hello"
		repo = "onebox-faas/hello"
		ref  = "0123456789abcdef0123456789abcdef01234567"
	)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &sourceRefSink{}
			tc.setup(sink)
			srv := httptest.NewServer(sink)
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			// Most sub-cases drive --json output so the response
			// is a single JSON object on stdout (not the SSE tail).
			// happy_path asserts on JSON stdout; retry_after /
			// not_found assert on stderr (no stdout pin); the CI
			// regression case wants a 202 + stdout JSON path.
			withResetJSONOutput(t, true)

			stdout, restore := captureStdout(t)
			defer restore()
			stderr, restoreErr := captureStderr(t)
			defer restoreErr()

			code := tc.invoke(slug, repo, ref, api.DeployAnnotations{})
			if code != tc.expect.exitCode {
				t.Errorf("exit = %d, want %d (stderr=%q)", code, tc.expect.exitCode, stderr.String())
			}
			for _, want := range tc.expect.stderrContains {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("expected %q in stderr, got %q", want, stderr.String())
				}
			}
			if tc.expect.stdoutJSONID != "" {
				var out api.DeploymentResponse
				if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out); err != nil {
					t.Fatalf("decode stdout: %v\n%s", err, stdout.String())
				}
				if out.ID != tc.expect.stdoutJSONID {
					t.Errorf("stdout deployment.id = %q, want %q", out.ID, tc.expect.stdoutJSONID)
				}
			}
			if tc.expect.wirePin != nil {
				tc.expect.wirePin(t, sink)
			}
		})
	}
}

// TestCmdDeployTarball_RefGuards covers Layer 1's --ref + slug
// validation guards at the dispatch layer (NOT the SDK method —
// that path is exercised by TestCmdDeployRepoSourceRef above).
// These two sub-cases reject before any HTTP call, so they don't
// need a sink or httptest server at all.
func TestCmdDeployTarball_RefGuards(t *testing.T) {
	cases := []struct {
		name            string
		args            []string
		wantExit        int
		wantStderrHas   string
		wantNoServerHit bool // true: sink must show 0 calls (the guard fires before any HTTP)
	}{
		{
			name: "missing_ref",
			args: []string{
				"--repo", "onebox-faas/hello",
				// no --ref
			},
			wantExit:        1,
			wantStderrHas:   "missing --ref",
			wantNoServerHit: true,
		},
		{
			name: "invalid_repo_slug",
			args: []string{
				"--repo", "bad slug with spaces",
				"--ref", "0123456789abcdef0123456789abcdef01234567",
			},
			wantExit:        1,
			wantStderrHas:   "Invalid --repo",
			wantNoServerHit: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Stand up a sink so a regression that reaches the wire
			// is caught (wantNoServerHit asserts the guard fires
			// before any HTTP call).
			sink := &sourceRefSink{}
			srv := httptest.NewServer(sink)
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			_, restore := captureStdout(t)
			defer restore()
			stderr, restoreErr := captureStderr(t)
			defer restoreErr()

			code := cmdDeployTarball(tc.args)
			if code != tc.wantExit {
				t.Errorf("exit = %d, want %d", code, tc.wantExit)
			}
			if !strings.Contains(stderr.String(), tc.wantStderrHas) {
				t.Errorf("expected %q in stderr, got %q", tc.wantStderrHas, stderr.String())
			}
			if tc.wantNoServerHit && sink.capturedCalls != 0 {
				t.Errorf("rejected call still reached the server: calls=%d", sink.capturedCalls)
			}
		})
	}
}

// TestCmdDeployRepoSourceRef_NoWait verifies that the shared deploy
// completion contract also applies to the GitHub source-ref path. Before this
// regression test, `deploy --repo ... --no-wait` ignored the flag and opened
// the SSE stream after the POST, so CI and scripts could block unexpectedly.
func TestCmdDeployRepoSourceRef_NoWait(t *testing.T) {
	sink := &sourceRefSink{
		status: http.StatusAccepted,
		body: api.DeploymentResponse{
			ID: "dep_nowait", AppID: "app_hello", BuildID: "build_nowait", Status: "queued",
		},
	}
	srv := httptest.NewServer(sink)
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	withResetJSONOutput(t, false)

	stdout, restoreOut := captureStdout(t)
	defer restoreOut()
	_, restoreErr := captureStderr(t)
	defer restoreErr()

	code := cmdDeployTarball([]string{
		"--name", "hello",
		"--repo", "onebox-faas/hello",
		"--ref", "main",
		"--no-wait",
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Deployment dep_nowait queued") {
		t.Fatalf("stdout = %q, want queued receipt", stdout.String())
	}
	if sink.capturedCalls != 1 {
		t.Fatalf("source-ref POST calls = %d, want 1 (no SSE follow-up)", sink.capturedCalls)
	}
}

// Compile-time guard: sourceRefSink implements http.Handler.
var _ http.Handler = (*sourceRefSink)(nil)

// Reference the context import so `go vet` doesn't complain when
// future sub-cases add ctx-aware behaviour.
var _ = context.Background
