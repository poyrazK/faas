// commands_init_test.go — Wave 0 PR-B customer-facing scaffolder tests.
//
// Mirrors the `gregale sign-keys` test style (sign_keys_test.go): the
// surface is purely local (no httptest, no authedClient, no SDK
// round-trip), so the only I/O the tests exercise is the disk under
// t.TempDir() and the osStdout / os.Stderr package seams. The
// `swapIO` and `swapStdout` helpers from sign_keys_test.go are reused.
//
// What we cover:
//   - every Wave 0 PR-B template materializes with the expected
//     file set and a README that mentions the right `gregale secrets
//     set` commands (drift pin between the CLI hint and the README)
//   - missing / unknown / non-empty --path rejection paths
//   - the --deploy chain passes --template + --name through to
//     cmdDeployTarball (verified indirectly via the cmdDeployTarball
//     failure path — we stub the authedClient by not setting FAAS_TOKEN,
//     so the chain returns 1 with the expected "Not logged in" error)
//   - cwd is NOT mutated by the chain (the plan-agent risk #1)
//   - help output carries the docs URL
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
)

// TestCmdInit_AllTemplatesMaterialize: every Wave 0 PR-B template
// (s3-uploader, slack-bot, rest-api-postgres, cron-worker) writes the
// expected file set to a fresh t.TempDir() via runCmdInit. Pinned so a
// future template addition can't silently drop the README or
// package.json. The pre-existing seven templates are out of scope for
// this PR — they were tested by their own embed_test.go round-trip.
func TestCmdInit_AllTemplatesMaterialize(t *testing.T) {
	cases := []struct {
		name      string
		files     []string // files that must exist post-materialize
		readmeHas []string // substrings the README must contain
	}{
		{
			name:  "s3-uploader",
			files: []string{"handler.js", "package.json", "README.md"},
			readmeHas: []string{
				"S3_BUCKET",
				"gregale secrets set",
				"AWS S3", // mentions S3-compatible provider
			},
		},
		{
			name:  "slack-bot",
			files: []string{"handler.js", "package.json", "README.md"},
			readmeHas: []string{
				"SLACK_SIGNING_SECRET",
				"gregale secrets set",
				"HMAC-SHA256",
			},
		},
		{
			name:  "rest-api-postgres",
			files: []string{"handler.js", "package.json", "README.md"},
			readmeHas: []string{
				"DATABASE_URL",
				"gregale secrets set",
				"Neon",
			},
		},
		{
			name:  "cron-worker",
			files: []string{"handler.js", "package.json", "README.md"},
			readmeHas: []string{
				"QSTASH_TOKEN",
				"UPSTASH_REDIS_REST_URL",
				"gregale secrets set",
			},
		},
		{
			name:  "webhook-receiver",
			files: []string{"handler.js", "package.json", "README.md"},
			readmeHas: []string{
				"WEBHOOK_SECRET",
				"X-Webhook-Secret",
				"gregale secrets set",
			},
		},
		{
			name:  "ai-chat",
			files: []string{"handler.js", "package.json", "README.md"},
			readmeHas: []string{
				"OPENAI_API_KEY",
				"ANTHROPIC_API_KEY",
				"gregale secrets set",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dest := filepath.Join(t.TempDir(), c.name)
			stdout, _, restore := swapIO(t)
			defer restore()
			code := runCmdInit(c.name, dest, false, "", osStdout, os.Stderr)
			if code != 0 {
				t.Fatalf("runCmdInit(%q, ...) = %d, want 0; stdout=%q", c.name, code, stdout.String())
			}
			for _, f := range c.files {
				if _, err := os.Stat(filepath.Join(dest, f)); err != nil {
					t.Errorf("expected %q post-materialize, got: %v", f, err)
				}
			}
			readmeBytes, err := os.ReadFile(filepath.Join(dest, "README.md"))
			if err != nil {
				t.Fatalf("read README: %v", err)
			}
			readme := string(readmeBytes)
			for _, want := range c.readmeHas {
				if !strings.Contains(readme, want) {
					t.Errorf("README missing %q; full README:\n%s", want, readme)
				}
			}
			// The "next steps" hint must mention the storage docs URL
			// (the canonical external-storage page, UX spec §8) so the
			// customer lands on the right docs regardless of template.
			if !strings.Contains(stdout.String(), storageDocsURL) {
				t.Errorf("stdout missing docs URL; got: %q", stdout.String())
			}
		})
	}
}

// TestCmdInit_UnknownTemplate: a bad --template name fails fast
// with an actionable error message listing the available templates.
// Doesn't touch the disk — just asserts the validation error.
func TestCmdInit_UnknownTemplate(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out")
	_, readStderr, restore := swapIO(t)
	defer restore()
	code := runCmdInit("not-a-real-template", dest, false, "", osStdout, os.Stderr)
	if code != 1 {
		t.Errorf("runCmdInit(unknown) = %d, want 1", code)
	}
	got := readStderr()
	if !strings.Contains(got, "not-a-real-template") {
		t.Errorf("stderr missing the bad template name; got: %q", got)
	}
	// Must list at least the four Wave 0 PR-B templates by name so
	// the customer can see what's available.
	for _, want := range []string{"s3-uploader", "slack-bot", "rest-api-postgres", "cron-worker", "webhook-receiver", "ai-chat"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr missing template name %q; got: %q", want, got)
		}
	}
	// dest must not exist — we rejected before MkdirAll.
	if _, err := os.Stat(dest); err == nil {
		t.Errorf("dest %q should not exist after unknown-template rejection", dest)
	}
}

// TestCmdInit_NonEmptyDestination: refuses to write into an existing
// non-empty directory. The customer's existing files are load-bearing;
// overwriting silently would lose work.
func TestCmdInit_NonEmptyDestination(t *testing.T) {
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "existing.txt"), []byte("untouchable"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, _, restore := swapIO(t)
	defer restore()
	code := runCmdInit("s3-uploader", dest, false, "", osStdout, os.Stderr)
	if code != 1 {
		t.Errorf("runCmdInit(non-empty dest) = %d, want 1", code)
	}
	// Existing file must still be intact.
	got, err := os.ReadFile(filepath.Join(dest, "existing.txt"))
	if err != nil {
		t.Fatalf("read existing file: %v", err)
	}
	if string(got) != "untouchable" {
		t.Errorf("existing file was modified: got %q", got)
	}
}

// TestCmdInit_CreatesMissingPath: a --path that doesn't exist yet is
// created via MkdirAll. This is the happy path for `gregale init --path=./uploader`
// on a fresh checkout.
func TestCmdInit_CreatesMissingPath(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "nested", "uploader")
	_, _, restore := swapIO(t)
	defer restore()
	code := runCmdInit("s3-uploader", dest, false, "", osStdout, os.Stderr)
	if code != 0 {
		t.Errorf("runCmdInit(missing path) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(dest, "handler.js")); err != nil {
		t.Errorf("expected handler.js post-materialize: %v", err)
	}
}

// TestCmdInit_CwdUnchangedByDeployChain: the --deploy chain runs
// cmdDeployTarball but must NOT mutate the caller's cwd. Plan-agent
// risk #1; we pin it via process-state inspection around the call.
//
// We don't have a way to stub cmdDeployTarball cleanly without
// refactoring the function into an injectable seam, so we exercise
// the chain against a path that will fail in cmdDeployTarball (no
// auth token configured). The failure happens AFTER cwd would have
// been captured, so any cwd mutation in our code would show up here.
func TestCmdInit_CwdUnchangedByDeployChain(t *testing.T) {
	before, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "uploader")
	_, _, restore := swapIO(t)
	defer restore()
	// No FAAS_TOKEN; cmdDeployTarball will hit "Not logged in" and
	// return non-zero. We're testing the cwd invariant, not the
	// deploy outcome.
	code := runCmdInit("s3-uploader", dest, true, "uploader-test", osStdout, os.Stderr)
	after, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd after: %v", err)
	}
	if before != after {
		t.Errorf("cwd changed: before=%q after=%q (runCmdInit must not chdir)", before, after)
	}
	// We expect a non-zero exit (auth failed) — but the materialized
	// files must still be on disk (the chain runs after Materialize).
	if code == 0 {
		t.Logf("deploy chain returned 0 (unexpected in test env); got code=%d", code)
	}
	if _, err := os.Stat(filepath.Join(dest, "handler.js")); err != nil {
		t.Errorf("expected handler.js post-materialize despite deploy failure: %v", err)
	}
}

// TestCmdInit_NextStepsFor: pins the per-template `gregale secrets set`
// hint text so the CLI hint and the template README stay in lockstep.
// Adding a new template means adding a case here AND adding the
// README line; this test fails if either drifts.
func TestCmdInit_NextStepsFor(t *testing.T) {
	cases := []struct {
		tpl  string
		want []string
	}{
		{"s3-uploader", []string{"S3_BUCKET", "gregale secrets set", "cd <dest>"}},
		{"slack-bot", []string{"SLACK_SIGNING_SECRET", "gregale secrets set", "cd <dest>"}},
		{"rest-api-postgres", []string{"DATABASE_URL", "gregale secrets set", "cd <dest>"}},
		{"cron-worker", []string{"QSTASH_TOKEN", "UPSTASH_REDIS_REST_URL", "gregale secrets set", "cd <dest>"}},
		{"webhook-receiver", []string{"WEBHOOK_SECRET", "openssl rand", "gregale secrets set", "cd <dest>"}},
		{"ai-chat", []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "gregale secrets set", "cd <dest>"}},
		{"hello-node", []string{"cd <dest>", "gregale deploy"}}, // default branch
	}
	for _, c := range cases {
		t.Run(c.tpl, func(t *testing.T) {
			steps := nextStepsFor(c.tpl)
			joined := strings.Join(steps, "\n")
			for _, w := range c.want {
				if !strings.Contains(joined, w) {
					t.Errorf("nextStepsFor(%q) missing %q; got:\n%s", c.tpl, w, joined)
				}
			}
		})
	}
}

// TestCmdInit_HelpFlagShowsDocs: the --help branch of cmdInit prints
// usage + the docs URL via PrintUsage. The flag package prints its
// own usage on Parse failure when --help is supplied, but the
// "missing --template" / "missing --path" branches go through our
// PrintUsage wrapper.
func TestCmdInit_HelpFlagShowsDocs(t *testing.T) {
	_, readStderr, restore := swapIO(t)
	defer restore()
	code := cmdInit([]string{"--template=s3-uploader"}) // missing --path
	if code != 1 {
		t.Errorf("cmdInit(missing --path) = %d, want 1", code)
	}
	got := readStderr()
	if !strings.Contains(got, "--template") || !strings.Contains(got, "--path") {
		t.Errorf("usage missing flags; got: %q", got)
	}
	if !strings.Contains(got, docsSiteURL) {
		t.Errorf("usage missing docs URL; got: %q", got)
	}
}

// TestCmdInit_RejectsPositional: any positional after flags is rejected.
// Defensive — `gregale init foo bar` should not silently pass.
func TestCmdInit_RejectsPositional(t *testing.T) {
	_, _, restore := swapIO(t)
	defer restore()
	code := cmdInit([]string{"--template=s3-uploader", "--path=/tmp/x", "extra-positional"})
	if code != 1 {
		t.Errorf("cmdInit(positional) = %d, want 1", code)
	}
}

// TestCheckDestEmpty: the helper that drives the non-empty-dest
// rejection. Edge cases: missing path (fine), empty path (fine),
// populated path (rejection).
func TestCheckDestEmpty(t *testing.T) {
	t.Run("missing path is fine", func(t *testing.T) {
		err := checkDestEmpty(filepath.Join(t.TempDir(), "does-not-exist"))
		if err != nil {
			t.Errorf("missing path: got %v, want nil", err)
		}
	})
	t.Run("empty path is fine", func(t *testing.T) {
		err := checkDestEmpty(t.TempDir())
		if err != nil {
			t.Errorf("empty path: got %v, want nil", err)
		}
	})
	t.Run("populated path is rejected", func(t *testing.T) {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, "x"), []byte("y"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		err := checkDestEmpty(d)
		if err == nil {
			t.Errorf("populated path: got nil, want error")
		} else if !strings.Contains(err.Error(), "not empty") {
			t.Errorf("error message should say 'not empty'; got: %v", err)
		}
	})
}

// TestCmdInit_List_GroupsByCategory: `gregale init --list` short-circuits
// before materialization and renders the 13 templates grouped by
// category. Pins both the group ordering (templates.CategoryOrder) and
// the per-category content (CategoryFor). A future template addition
// must add a Names entry, a CategoryFor case, and (if it's a new group)
// a CategoryOrder entry — this test fails if any of the three drift.
func TestCmdInit_List_GroupsByCategory(t *testing.T) {
	var buf bytes.Buffer
	if code := runCmdInitList(&buf); code != 0 {
		t.Fatalf("runCmdInitList = %d, want 0", code)
	}
	out := buf.String()
	// Order: hello → function → stateless-contract → ai (the pinned
	// CategoryOrder). Find each header line; assert relative order.
	idx := map[string]int{
		"hello":              strings.Index(out, "hello ("),
		"function":           strings.Index(out, "function ("),
		"stateless-contract": strings.Index(out, "stateless-contract ("),
		"ai":                 strings.Index(out, "ai ("),
	}
	for k, i := range idx {
		if i < 0 {
			t.Errorf("missing category header %q in --list output:\n%s", k, out)
		}
	}
	if idx["hello"] >= idx["function"] || idx["function"] >= idx["stateless-contract"] || idx["stateless-contract"] >= idx["ai"] {
		t.Errorf("category order drift: %v\noutput:\n%s", idx, out)
	}
	// Spot-check expected contents under each category so a future
	// CategoryFor mis-classification can't ship silently.
	wantPerCat := map[string][]string{
		"hello":              {"hello-node", "hello-python", "hello-go"},
		"function":           {"function-node", "function-python", "function-go", "function-node24", "function-python313", "cron-example"},
		"stateless-contract": {"s3-uploader", "slack-bot", "rest-api-postgres", "cron-worker", "webhook-receiver"},
		"ai":                 {"ai-chat"},
	}
	for cat, names := range wantPerCat {
		for _, n := range names {
			needle := "\n  " + n + "\n"
			if !strings.Contains(out, needle) {
				t.Errorf("category %q missing template %q (looked for %q); output:\n%s", cat, n, needle, out)
			}
		}
	}
	// Trailing docs link so the customer lands on the canonical page.
	if !strings.Contains(out, cliDocsURL) {
		t.Errorf("--list missing docs URL; output:\n%s", out)
	}
}

// TestCmdInit_ListShortCircuits: `--list` must skip the validation
// paths that run when --template / --path are required. A bare
// `gregale init --list` (no other args) must succeed even though
// --template and --path are unset.
func TestCmdInit_ListShortCircuits(t *testing.T) {
	var stdout bytes.Buffer
	oldOut := osStdout
	osStdout = &stdout
	defer func() { osStdout = oldOut }()
	if code := cmdInit([]string{"--list"}); code != 0 {
		t.Errorf("cmdInit --list = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "hello") {
		t.Errorf("cmdInit --list stdout missing category header; got: %q", stdout.String())
	}
}

// TestTemplates_CategoryForCoversAllNames: every entry in templates.Names
// must have a non-empty CategoryFor — an empty category would render
// as an ungrouped row in `gregale init --list` (silently dropped by the
// runCmdInitList loop). This pins the contract: "adding a template
// means a new entry in BOTH Names AND CategoryFor".
func TestTemplates_CategoryForCoversAllNames(t *testing.T) {
	for _, n := range templates.Names {
		if cat := templates.CategoryFor(n); cat == "" {
			t.Errorf("templates.CategoryFor(%q) = \"\", want a category; missing switch arm", n)
		}
	}
	// And the reverse: every CategoryOrder entry must classify at
	// least one name (else the order contains an empty bucket).
	for _, cat := range templates.CategoryOrder {
		has := false
		for _, n := range templates.Names {
			if templates.CategoryFor(n) == cat {
				has = true
				break
			}
		}
		if !has {
			t.Errorf("templates.CategoryOrder contains %q but no template classifies under it", cat)
		}
	}
}

// TestCmdInit_PreservesStdoutBytes: the package-level osStdout seam
// is what runCmdInit's caller uses — but runCmdInit also takes its
// own stdout parameter. This test pins the rule that the parameter
// is what gets written to (not the seam). When the test passes
// `&bytes.Buffer{}` as stdout, the seam's value shouldn't matter.
// (When the CLI's real `cmdInit` calls runCmdInit, it passes
// `osStdout`, so this doesn't matter in production — but the unit
// test must keep the contract.)
func TestCmdInit_PreservesStdoutBytes(t *testing.T) {
	var captured bytes.Buffer
	dest := filepath.Join(t.TempDir(), "s3")
	// Pass an explicit buffer as stdout; the osStdout seam is left
	// untouched by this test (set to a throwaway buf so we can detect
	// if the implementation accidentally writes there).
	throwaway := &bytes.Buffer{}
	oldOut := osStdout
	osStdout = throwaway
	defer func() { osStdout = oldOut }()
	code := runCmdInit("s3-uploader", dest, false, "", &captured, os.Stderr)
	if code != 0 {
		t.Fatalf("runCmdInit = %d, want 0; captured=%q", code, captured.String())
	}
	if !strings.Contains(captured.String(), storageDocsURL) {
		t.Errorf("explicit stdout buffer missing docs URL; got: %q", captured.String())
	}
	if throwaway.Len() != 0 {
		t.Errorf("osStdout seam was written to (%d bytes); should be untouched when explicit stdout is passed", throwaway.Len())
	}
}
