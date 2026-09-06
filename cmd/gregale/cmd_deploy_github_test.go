// cmd_deploy_github_test.go — whitebox tests for the
// `gregale deploy --github` snippet generator (issue #270).
//
// All sub-cases are table-driven under TestRenderGithubSnippet so the
// package-level test binary builds the fixture once. The cases pin
// the load-bearing seams of the snippet body:
//
//   - bare invocation (no env) → `${{ github.* }}` expressions
//   - runner env (GITHUB_REPOSITORY + GITHUB_SHA) → concrete values
//   - explicit CLI overrides (--repo / --ref) → concrete values
//   - pinned SHA → `# pin:` comment line points at the SHA
//   - missing app → CLI exit 1 (cmdDeployGithubSnippet path)
//
// The test does NOT exercise the Action's vendored binary — that's
// the e2e fixture in poyrazK/faas/.github/actions/deploy/test/e2e. This
// suite is the snippet-shape contract.

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderGithubSnippet(t *testing.T) {
	cases := []struct {
		name      string
		env       githubSnippetEnv
		app       string
		pinnedSHA string
		mustLines []string // substrings that MUST appear
		mustNotLn []string // substrings that MUST NOT appear
	}{
		{
			name: "bare invocation emits ${{ github.* }} placeholders",
			env:  githubSnippetEnv{Runner: false},
			app:  "my-app",
			mustLines: []string{
				"# Gregale deploy",
				"# App: my-app",
				"Repo: ${{ github.repository }}",
				"Ref: ${{ github.sha }}",
				"app: my-app",
				"uses: poyrazK/faas/.github/actions/deploy@v1",
				"id-token: write",
				"checks: write",
				"https://api.gregale.dev",
				"wait: \"false\"",
			},
			mustNotLn: []string{
				"# pin this Action", // no SHA provided → no pin comment
				"api-key:",
			},
		},
		{
			name: "runner env hard-codes GITHUB_REPOSITORY + GITHUB_SHA",
			env: githubSnippetEnv{
				Repository: "onebox-faas/hello",
				SHA:        "a1b2c3d4e5f6789012345678901234567890abcd",
				Runner:     true,
			},
			app: "hello",
			mustLines: []string{
				"Repo: onebox-faas/hello",
				"Ref: a1b2c3d4e5f6789012345678901234567890abcd",
				"app: hello",
				// The body uses the env values directly — the
				// `${{ github.* }}` expressions do NOT appear in the
				// rendered snippet when the runner env is set.
			},
			mustNotLn: []string{
				"Repo: ${{ github.repository }}",
				"Ref: ${{ github.sha }}",
			},
		},
		{
			name:      "pinned SHA emits # pin: <sha> comment line",
			env:       githubSnippetEnv{Runner: false},
			app:       "my-app",
			pinnedSHA: "f1e2d3c4b5a6987654321098765432109abcdef0",
			mustLines: []string{
				"# pin this Action for reproducibility: poyrazK/faas/.github/actions/deploy@f1e2d3c4b5a6987654321098765432109abcdef0",
				"uses: poyrazK/faas/.github/actions/deploy@v1",
			},
		},
		{
			name: "runner env + pinned SHA coexist",
			env: githubSnippetEnv{
				Repository: "onebox-faas/hello",
				SHA:        "a1b2c3d4e5f6789012345678901234567890abcd",
				Runner:     true,
			},
			app:       "hello",
			pinnedSHA: "f1e2d3c4b5a6987654321098765432109abcdef0",
			mustLines: []string{
				"Repo: onebox-faas/hello",
				"# pin this Action for reproducibility: poyrazK/faas/.github/actions/deploy@f1e2d3c4b5a6987654321098765432109abcdef0",
				"app: hello",
				"uses: poyrazK/faas/.github/actions/deploy@v1",
			},
		},
		{
			name: "OIDC is the default and no long-lived secret is emitted",
			env:  githubSnippetEnv{Runner: true, Repository: "acme/widget", SHA: "0000000000000000000000000000000000000000"},
			app:  "widget",
			mustLines: []string{
				"id-token: write",
			},
			mustNotLn: []string{
				// The default snippet requests a short-lived GitHub OIDC
				// token and never embeds or references a deploy secret.
				"${{ secrets.GREGALE_API_KEY }}",
				"api-key:",
				"api-key: ghp_",
				"api-key: gho_",
				"api-key: FAAS_TOKEN=",
			},
		},
		{
			// Per-field concrete override (--repo only). The
			// customer said "use this repo" but didn't override
			// the ref; the ref MUST stay a `${{ github.sha }}`
			// placeholder rather than collapsing to the empty
			// string from an unset GITHUB_SHA. We assert against
			// the header line (# App: ... · Repo: ... · Ref: ...)
			// specifically — the body contains a static comment
			// with `${{ github.sha }}` regardless of the header.
			name: "ConcreteRepository only flips Repo, leaves Ref as ${{ github.sha }}",
			env: githubSnippetEnv{
				ConcreteRepository: "acme/widget",
			},
			app: "widget",
			mustLines: []string{
				"# App: widget · Repo: acme/widget · Ref: ${{ github.sha }}",
			},
			mustNotLn: []string{
				// The repo-only override must NOT trigger Runner
				// mode (which would blank the Ref side).
				"· Repo:  ·",
				"· Repo:  ",
			},
		},
		{
			// Per-field concrete override (--ref only). Symmetric
			// case to the repo-only override.
			name: "ConcreteSHA only flips Ref, leaves Repo as ${{ github.repository }}",
			env: githubSnippetEnv{
				ConcreteSHA: "a1b2c3d4e5f6789012345678901234567890abcd",
			},
			app: "widget",
			mustLines: []string{
				"# App: widget · Repo: ${{ github.repository }} · Ref: a1b2c3d4e5f6789012345678901234567890abcd",
			},
		},
		{
			// Both per-field overrides. Independent of Runner mode.
			name: "ConcreteRepository + ConcreteSHA both flip, Runner=false",
			env: githubSnippetEnv{
				ConcreteRepository: "acme/widget",
				ConcreteSHA:        "a1b2c3d4e5f6789012345678901234567890abcd",
			},
			app: "widget",
			mustLines: []string{
				"# App: widget · Repo: acme/widget · Ref: a1b2c3d4e5f6789012345678901234567890abcd",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderGithubSnippet(tc.env, tc.app, tc.pinnedSHA)
			if !strings.HasPrefix(got, "# Gregale deploy") {
				t.Fatalf("snippet missing leading sentinel; got:\n%s", got)
			}
			for _, want := range tc.mustLines {
				if !strings.Contains(got, want) {
					t.Errorf("snippet missing required line %q\nfull output:\n%s", want, got)
				}
			}
			for _, ban := range tc.mustNotLn {
				if strings.Contains(got, ban) {
					t.Errorf("snippet must NOT contain %q\nfull output:\n%s", ban, got)
				}
			}
		})
	}
}

func TestDetectGithubSnippetEnv(t *testing.T) {
	// Save and restore the env vars so we don't leak state across
	// parallel tests in the same package.
	t.Setenv("GITHUB_REPOSITORY", "")
	t.Setenv("GITHUB_SHA", "")

	// Bare: both unset → not a runner.
	if got := detectGithubSnippetEnv(); got.Runner {
		t.Errorf("bare env: Runner=true, want false (env=%+v)", got)
	}

	// Partial: only repo → not a runner (we require BOTH).
	t.Setenv("GITHUB_REPOSITORY", "acme/widget")
	if got := detectGithubSnippetEnv(); got.Runner {
		t.Errorf("partial env: Runner=true, want false (env=%+v)", got)
	}

	// Full: both set → runner.
	t.Setenv("GITHUB_SHA", "a1b2c3d4e5f6789012345678901234567890abcd")
	got := detectGithubSnippetEnv()
	if !got.Runner {
		t.Errorf("full env: Runner=false, want true (env=%+v)", got)
	}
	if got.Repository != "acme/widget" {
		t.Errorf("Repository: got %q, want %q", got.Repository, "acme/widget")
	}
	if got.SHA != "a1b2c3d4e5f6789012345678901234567890abcd" {
		t.Errorf("SHA: got %q, want %q", got.SHA, "a1b2c3d4e5f6789012345678901234567890abcd")
	}
}

func TestCmdDeployGithubSnippet(t *testing.T) {
	t.Run("missing --app exits 1", func(t *testing.T) {
		var buf bytes.Buffer
		oldOut := osStdout
		oldErr := osStderr
		osStdout = &buf
		osStderr = &buf
		defer func() {
			osStdout = oldOut
			osStderr = oldErr
		}()

		// No --app → exit 1.
		if code := cmdDeployGithubSnippet([]string{}); code != 1 {
			t.Errorf("missing --app: exit code = %d, want 1", code)
		}
		if !strings.Contains(buf.String(), "missing --app") {
			t.Errorf("missing --app: stderr wanted 'missing --app', got: %q", buf.String())
		}
	})

	t.Run("happy path prints snippet to stdout", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		oldOut := osStdout
		oldErr := osStderr
		osStdout = &stdout
		osStderr = &stderr
		defer func() {
			osStdout = oldOut
			osStderr = oldErr
		}()

		// Clear env so the snippet uses ${{ github.* }} placeholders.
		t.Setenv("GITHUB_REPOSITORY", "")
		t.Setenv("GITHUB_SHA", "")

		if code := cmdDeployGithubSnippet([]string{"--app", "my-app"}); code != 0 {
			t.Errorf("happy path: exit code = %d, want 0; stderr=%q", code, stderr.String())
		}
		out := stdout.String()
		if !strings.HasPrefix(out, "# Gregale deploy") {
			t.Errorf("happy path: stdout missing sentinel; got:\n%s", out)
		}
		if !strings.Contains(out, "app: my-app") {
			t.Errorf("happy path: stdout missing app slug; got:\n%s", out)
		}
		if stderr.Len() != 0 {
			t.Errorf("happy path: stderr should be empty; got %q", stderr.String())
		}
	})

	t.Run("runner env produces concrete values", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		oldOut := osStdout
		oldErr := osStderr
		osStdout = &stdout
		osStderr = &stderr
		defer func() {
			osStdout = oldOut
			osStderr = oldErr
		}()

		t.Setenv("GITHUB_REPOSITORY", "acme/widget")
		t.Setenv("GITHUB_SHA", "a1b2c3d4e5f6789012345678901234567890abcd")

		if code := cmdDeployGithubSnippet([]string{"--app", "widget"}); code != 0 {
			t.Errorf("runner env: exit code = %d, want 0", code)
		}
		out := stdout.String()
		// The header line is "# App: <app> · Repo: <repo> · Ref: <sha>" —
		// assert substrings that actually appear in that line, not the
		// full "# Repo:" prefix (which is split by the "·" separator).
		if !strings.Contains(out, "Repo: acme/widget") {
			t.Errorf("runner env: stdout missing concrete repo; got:\n%s", out)
		}
		if !strings.Contains(out, "Ref: a1b2c3d4e5f6789012345678901234567890abcd") {
			t.Errorf("runner env: stdout missing concrete SHA; got:\n%s", out)
		}
		if strings.Contains(out, "repo: ${{ github.repository }}") {
			t.Errorf("runner env: stdout still contains ${{ github.repository }} expression; got:\n%s", out)
		}
	})

	t.Run("--repo only flips Repo, leaves Ref as ${{ github.sha }}", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		oldOut := osStdout
		oldErr := osStderr
		osStdout = &stdout
		osStderr = &stderr
		defer func() {
			osStdout = oldOut
			osStderr = oldErr
		}()

		// Bare env (no GITHUB_REPOSITORY / GITHUB_SHA). The CLI
		// override must force Repo concrete without blanking Ref.
		t.Setenv("GITHUB_REPOSITORY", "")
		t.Setenv("GITHUB_SHA", "")

		if code := cmdDeployGithubSnippet([]string{"--app", "widget", "--repo", "acme/widget"}); code != 0 {
			t.Errorf("--repo only: exit code = %d, want 0; stderr=%q", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Repo: acme/widget") {
			t.Errorf("--repo only: stdout missing concrete repo; got:\n%s", out)
		}
		if !strings.Contains(out, "Ref: ${{ github.sha }}") {
			t.Errorf("--repo only: stdout should keep Ref placeholder; got:\n%s", out)
		}
	})

	t.Run("--ref only flips Ref, leaves Repo as ${{ github.repository }}", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		oldOut := osStdout
		oldErr := osStderr
		osStdout = &stdout
		osStderr = &stderr
		defer func() {
			osStdout = oldOut
			osStderr = oldErr
		}()

		t.Setenv("GITHUB_REPOSITORY", "")
		t.Setenv("GITHUB_SHA", "")

		if code := cmdDeployGithubSnippet([]string{"--app", "widget", "--ref", "feature-branch"}); code != 0 {
			t.Errorf("--ref only: exit code = %d, want 0; stderr=%q", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "Ref: feature-branch") {
			t.Errorf("--ref only: stdout missing concrete ref; got:\n%s", out)
		}
		if !strings.Contains(out, "Repo: ${{ github.repository }}") {
			t.Errorf("--ref only: stdout should keep Repo placeholder; got:\n%s", out)
		}
	})
}
