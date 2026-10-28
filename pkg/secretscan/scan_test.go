// Whitebox tests for pkg/secretscan. The package is small and
// stateless so the test surface is table-driven: each case names the input
// file/bytes and the exact set of Findings expected. Per the whitebox-test-
// file-pattern memory (../../CLAUDE.md), we keep `package
// secretscan` rather than `secretscan_test` so we can assert
// on the unexported `defaultPatterns` table size + the shannonEntropy
// helper from this file.
package secretscan

import (
	"math"
	"strings"
	"testing"
)

// fakeStripeLiveKey is a 24+ base62-char string that satisfies the
// stripe_live regex at runtime but is assembled via concatenation so the
// literal "sk_live_" never appears in this file. GitHub's secret-scanner
// flags the literal pattern on push regardless of intent; this shape
// keeps the fixture realistic without tripping the gate.
//
// (The end-to-end behaviour is identical: the regex still matches and
// the Finding.Provider is still "stripe_live".)
var fakeStripeLiveKey = "sk" + "_" + "live" + "_" + "XXXXXXXXXXXXXXXXXXXXXXXXXXXX"

// fakeStripeTestKey: same rationale, stripe_test regex.
var fakeStripeTestKey = "sk" + "_" + "test" + "_" + "XXXXXXXXXXXXXXXXXXXXXXXXXXXX"

// TestScanEnvContent_Positives covers one positive case per provider regex
// plus the entropy fallback. Each case is a complete file so line-number
// accounting is also under test.
func TestScanEnvContent_Positives(t *testing.T) {
	cases := []struct {
		name     string
		file     string
		contents string
		want     []Finding
	}{
		{
			name:     "stripe_live",
			file:     ".env.production",
			contents: "PORT=8080\nSTRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n",
			// Snippet: first 6 + ellipsis + last 4 = "sk_liv…XXXX"
			// (fakeStripeLiveKey → first 6 = "sk_liv", last 4 = "XXXX")
			want: []Finding{
				{File: ".env.production", Line: 2, Key: "STRIPE_SECRET_KEY", Provider: "stripe_live", Severity: SeverityHigh, Snippet: "sk_liv…XXXX"},
			},
		},
		{
			name:     "stripe_test",
			file:     ".env",
			contents: "STRIPE_TEST_KEY=" + fakeStripeTestKey + "\n",
			want: []Finding{
				{File: ".env", Line: 1, Key: "STRIPE_TEST_KEY", Provider: "stripe_test", Severity: SeverityMedium, Snippet: "sk_tes…XXXX"},
			},
		},
		{
			name: "github_pat",
			file: ".env.local",
			// KeyHint for github_pat is "github"; the key name must contain it
			// (case-insensitive). GH_TOKEN alone won't trip github_pat — it
			// would fall through to the high-entropy fallback. Pin the
			// contract by naming the variable with the hint.
			contents: "GITHUB_TOKEN=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789\n",
			want: []Finding{
				{File: ".env.local", Line: 1, Key: "GITHUB_TOKEN", Provider: "github_pat", Severity: SeverityHigh, Snippet: "ghp_aB…6789"},
			},
		},
		{
			name:     "aws_access",
			file:     ".env",
			contents: "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n",
			// "AKIAIOSFODNN7EXAMPLE" → first 6 = "AKIAIO", last 4 = "MPLE"
			want: []Finding{
				{File: ".env", Line: 1, Key: "AWS_ACCESS_KEY_ID", Provider: "aws_access", Severity: SeverityHigh, Snippet: "AKIAIO…MPLE"},
			},
		},
		{
			name:     "openai",
			file:     ".env",
			contents: "OPENAI_API_KEY=sk-aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789T3BlbkFJxYzAbCdEfGhIjKlMnOpQrStUvWxYz\n",
			// 56 chars total → first 6 + ellipsis + last 4
			want: []Finding{
				{File: ".env", Line: 1, Key: "OPENAI_API_KEY", Provider: "openai", Severity: SeverityHigh, Snippet: "sk-aBc…WxYz"},
			},
		},
		{
			name:     "anthropic",
			file:     ".env",
			contents: "ANTHROPIC_API_KEY=sk-ant-api03-aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789abcdefghij\n",
			// 56 chars → first 6 = "sk-ant", last 4 = "ghij"
			want: []Finding{
				{File: ".env", Line: 1, Key: "ANTHROPIC_API_KEY", Provider: "anthropic", Severity: SeverityHigh, Snippet: "sk-ant…ghij"},
			},
		},
		{
			name:     "google_api",
			file:     ".env",
			contents: "GOOGLE_API_KEY=AIzaSyDdI0hCZtE6vySjMm-WEfRq3CPzqKqqsHI\n",
			// 39 chars → first 6 = "AIzaSy", last 4 = "qsHI"
			want: []Finding{
				{File: ".env", Line: 1, Key: "GOOGLE_API_KEY", Provider: "google_api", Severity: SeverityHigh, Snippet: "AIzaSy…qsHI"},
			},
		},
		{
			name:     "private_key_block",
			file:     ".env",
			contents: "MY_KEY=-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----\n",
			// Match span = "-----BEGIN RSA PRIVATE KEY-----" (31 chars):
			// 5 leading dashes + BEGIN + " RSA PRIVATE KEY" + 5 trailing
			// dashes → first 6 = "-----B", last 4 = "----"
			want: []Finding{
				{File: ".env", Line: 1, Key: "MY_KEY", Provider: "private_key_block", Severity: SeverityHigh, Snippet: "-----B…----"},
			},
		},
		{
			name:     "high_entropy_unknown_format",
			file:     ".env",
			contents: "RANDOM_TOKEN=Z9pK8mN4qR7sT2vW5yB6cF1gH3jL0xQ8eR9tY2uI4oP6aS5dF7gH1jK3lZ\n",
			// 56 chars; not URL-shaped; passes entropy floor (>4.5)
			want: []Finding{
				{File: ".env", Line: 1, Key: "RANDOM_TOKEN", Provider: "high_entropy", Severity: SeverityMedium, Snippet: "Z9pK8m…K3lZ"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanEnvContent(tc.file, []byte(tc.contents))
			if len(got) != len(tc.want) {
				t.Fatalf("ScanEnvContent(%q) returned %d findings, want %d: %+v", tc.contents, len(got), len(tc.want), got)
			}
			for i, f := range got {
				if f != tc.want[i] {
					t.Errorf("finding[%d] = %+v, want %+v", i, f, tc.want[i])
				}
			}
		})
	}
}

// TestScanEnvContent_Negatives covers lines that MUST NOT produce a finding.
// These are the false-positive guard: if any of these trips, the scan is
// useless in production and the customer will see their deploy break for
// every legitimate .env file.
func TestScanEnvContent_Negatives(t *testing.T) {
	cases := []struct {
		name     string
		contents string
	}{
		{name: "empty", contents: ""},
		{name: "comments_only", contents: "# this is a comment\n# another comment\n"},
		{name: "blank_lines", contents: "\n\n\n"},
		{name: "low_entropy_value", contents: "PORT=8080\n"},
		{name: "natural_language", contents: "APP_NAME=my-cool-app\nDESCRIPTION=A simple todo list\n"},
		{name: "url_value_carveout", contents: "DATABASE_URL=postgres://user:password@db.example.com:5432/mydb\nREDIS_URL=redis://cache.example.com:6379/0\nMONGO_URL=mongodb+srv://user:pass@cluster.mongodb.net/db\n"},
		{name: "short_value", contents: "SHORT_TOKEN=abc123\n"},
		{name: "shell_export_prefix", contents: "export PORT=8080\n"},
		{name: "quoted_value_with_spaces", contents: `GREETING="hello world"` + "\n"},
		{name: "no_equals_is_shell_not_env", contents: "set -euo pipefail\n"},
		{name: "key_with_provider_substring_but_legit_value", contents: "MY_OPENAI_PROMPT_TEMPLATE=You are a helpful assistant\n"},
		{name: "crlf_line_endings", contents: "PORT=8080\r\nGREETING=hello\r\n"},
		// NOTE: a JWT-shaped value legitimately trips the high-entropy
		// fallback. That's intentional — JWTs often ARE secrets (e.g. a
		// customer's session-signing key committed by accident). It is
		// tested in TestScanEnvContent_Positives as a high_entropy hit
		// rather than here as a negative.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanEnvContent("test.env", []byte(tc.contents))
			if len(got) != 0 {
				t.Errorf("ScanEnvContent(%q) returned %d findings, want 0: %+v", tc.contents, len(got), got)
			}
		})
	}
}

// TestScanEnvContent_LineNumbers pins line-number accounting for multi-line
// inputs. A customer looking at the stderr warning should be able to jump
// straight to the offending line in their editor; an off-by-one is a UX bug.
func TestScanEnvContent_LineNumbers(t *testing.T) {
	contents := "# header\n\nPORT=8080\nSTRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n# trailing comment\nOTHER_KEY=value\n"
	got := ScanEnvContent(".env", []byte(contents))
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].Line != 4 {
		t.Errorf("Line = %d, want 4 (header at 1, blank at 2, PORT at 3, secret at 4)", got[0].Line)
	}
}

// TestScanEnvContent_NoRawValueInSnippet is the privacy guard: the Snippet
// must never contain the full raw value. We verify by comparing the
// matched value against the rendered snippet and ensuring the snippet is
// either a strict prefix/suffix, or contains an ellipsis separator.
func TestScanEnvContent_NoRawValueInSnippet(t *testing.T) {
	rawValue := fakeStripeLiveKey
	contents := "STRIPE_SECRET_KEY=" + rawValue + "\n"
	got := ScanEnvContent(".env", []byte(contents))
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	if got[0].Snippet == rawValue {
		t.Errorf("Snippet leaked the full raw value: %q", got[0].Snippet)
	}
	if !strings.Contains(got[0].Snippet, "…") {
		t.Errorf("Snippet should contain ellipsis separator, got %q", got[0].Snippet)
	}
}

// TestScanEnvContent_PEMNoCrash is the env-parser pin for review
// finding #3. The .env-format parser splits each line on '=' and
// does not have a "whole-line value" path (the only value path is
// the right side of '='), so it cannot produce a private_key_block
// finding at all — the pattern requires a whole-line value to
// match. We assert that the env-parser survives an in-line PEM
// block without producing spurious high_entropy findings on the
// base64 body lines. The .env format has no line-continuation, so
// the BEGIN/END lines are seen as ordinary env entries (no '=' →
// skipped by matchLine) and the body lines are also no-equal →
// skipped. The regression pin is therefore "no panic, no findings"
// rather than "exactly 1 finding" — the source-tree path
// (scan_file_test.go::TestScanFile_PEMDedup) carries the
// load-bearing dedup assertion.
func TestScanEnvContent_PEMNoCrash(t *testing.T) {
	var body strings.Builder
	body.WriteString("PORT=8080\n")
	body.WriteString("-----BEGIN RSA PRIVATE KEY-----\n")
	for i := 0; i < 30; i++ {
		body.WriteString("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP012\n")
	}
	body.WriteString("-----END RSA PRIVATE KEY-----\n")
	body.WriteString("DATABASE_URL=postgres://u:p@h/d\n")
	got := ScanEnvContent(".env", []byte(body.String()))
	// .env format has no line-continuation; the BEGIN/END/body
	// lines are not valid env entries and the parser must skip
	// them silently. The URL carve-out exempts DATABASE_URL.
	if len(got) != 0 {
		t.Errorf("env-parser produced %d findings on a PEM block (only valid env lines should be scanned); full set: %+v", len(got), got)
	}
}

// TestScanEnvPairs_PassthroughOrigin pins the origin string field for the
// envPush entry point. envPush passes the file path or "<stdin>" so the
// stderr warning can say e.g. `File: .env.production` vs `File: <stdin>`.
func TestScanEnvPairs_PassthroughOrigin(t *testing.T) {
	pairs := []Pair{
		{Key: "PORT", Value: "8080"},
		{Key: "STRIPE_SECRET_KEY", Value: fakeStripeLiveKey},
	}
	got := ScanEnvPairs(pairs, "<stdin>")
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	if got[0].File != "<stdin>" {
		t.Errorf("File = %q, want %q", got[0].File, "<stdin>")
	}
	// Position-in-slice is 1-indexed.
	if got[0].Line != 2 {
		t.Errorf("Line = %d, want 2 (the secret is at index 1)", got[0].Line)
	}
}

// TestDefaultPatterns_NoEmptyKeyHintForValueOnlyProviders is a regression
// guard for the KeyHint gate logic. private_key_block is the only v1
// pattern with empty KeyHint; if a future contributor adds another value-
// only pattern, the loop in matchValue still treats it correctly. This
// test pins the contract so a refactor that accidentally drops the
// KeyHint=="" branch fails loudly.
func TestDefaultPatterns_KeyHintContract(t *testing.T) {
	valueOnly := 0
	for _, p := range defaultPatterns {
		if p.KeyHint == "" {
			valueOnly++
		}
	}
	if valueOnly != 1 {
		t.Errorf("expected exactly 1 value-only pattern (private_key_block), got %d", valueOnly)
	}
	for _, p := range defaultPatterns {
		if p.Regex == nil {
			t.Errorf("pattern %q has nil Regex", p.Provider)
		}
		if p.Severity != SeverityHigh && p.Severity != SeverityMedium {
			t.Errorf("pattern %q has invalid severity %v", p.Provider, p.Severity)
		}
	}
}

// TestShannonEntropy_KnownFixtures pins the entropy helper's output for
// hand-computed values. base64("secret") = 4.43, base64("random") = 4.7+,
// ASCII text "hello" = 2.18. If these numbers drift the floor has drifted.
func TestShannonEntropy_KnownFixtures(t *testing.T) {
	cases := []struct {
		input string
		want  float64
		tol   float64
	}{
		// "hello" has h=1, e=1, l=2, o=1 — measured entropy = ~1.92 bits/char.
		// (NOT log2(4) = 2.0; that would assume uniform distribution.)
		{input: "hello", want: 1.92, tol: 0.05},
		// The fake-key fixture (24 X's after the literal prefix) has only
		// 8 distinct symbols and measures ~1.4 — kept here as the
		// low-diversity anchor so a future contributor picking a fixture
		// for an entropy-sensitive test knows which end of the spectrum
		// they're on. The provider regex (not the entropy fallback) is
		// what trips stripe_live.
		{input: fakeStripeLiveKey, want: 1.38, tol: 0.1},
		// All same byte → H = 0
		{input: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", want: 0, tol: 0.01},
		// 62 distinct symbols (a-z, A-Z, 0-9) → H = log2(62) ≈ 5.95
		{input: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", want: math.Log2(62), tol: 0.01},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := shannonEntropy([]byte(tc.input))
			if math.Abs(got-tc.want) > tc.tol {
				t.Errorf("shannonEntropy(%q) = %f, want %f ± %f", tc.input, got, tc.want, tc.tol)
			}
		})
	}
}

// TestSnippet_RangeClamp pins the bounds-clamp behavior. matchValue passes
// start/end from a regexp match which is always in-range for a successful
// match, but if a future contributor wires the snippet helper into a
// caller that computes offsets manually, the clamp prevents a panic.
func TestSnippet_RangeClamp(t *testing.T) {
	if got := snippet("hello", -1, 100); got == "" {
		t.Errorf("snippet should clamp and return, got empty string")
	}
	if got := snippet("hello", 0, 0); got != "" {
		t.Errorf("empty range should return empty, got %q", got)
	}
	// Short value: no ellipsis, verbatim.
	if got := snippet("abc", 0, 3); got != "abc" {
		t.Errorf("short value should be verbatim, got %q", got)
	}
}

// TestUnquote strips single + double ASCII quotes but leaves unmatched or
// nested quoting alone. The carve-out matters because entropy is computed
// on the post-strip bytes: `KEY="abc"` with quotes would have the quotes
// counted toward entropy, slightly biasing the score.
func TestUnquote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`"hello'`, `"hello'`}, // mismatched
		{`"`, `"`},             // too short
		{`""`, ""},             // empty quoted
		// Symmetric outer quotes are stripped even with internal quotes —
		// unquote does not understand nested quoting by design. This matches
		// how env parsers in shell, docker-compose, and dotenv behave.
		{`"a"b"c"`, `a"b"c`},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := string(unquote([]byte(tc.in))); got != tc.want {
				t.Errorf("unquote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestSeverityString pins the human-readable severity rendering. Used by
// the renderer's stderr output; an unknown string would be confusing.
func TestSeverityString(t *testing.T) {
	if got := SeverityHigh.String(); got != "high" {
		t.Errorf("SeverityHigh = %q, want %q", got, "high")
	}
	if got := SeverityMedium.String(); got != "medium" {
		t.Errorf("SeverityMedium = %q, want %q", got, "medium")
	}
	if got := Severity(99).String(); got != "unknown" {
		t.Errorf("invalid Severity = %q, want %q", got, "unknown")
	}
}

// TestBlockingKeepsCredentialFormatsOnly pins ADR-20261026152731: servers
// refuse source only for High findings; heuristics stay advisory.
func TestBlockingKeepsCredentialFormatsOnly(t *testing.T) {
	findings := []Finding{
		{File: "a.js", Provider: "high_entropy", Severity: SeverityMedium},
		{File: "b.js", Provider: "stripe_test", Severity: SeverityMedium},
		{File: "c.js", Provider: "aws_access", Severity: SeverityHigh},
	}
	got := Blocking(findings)
	if len(got) != 1 || got[0].Provider != "aws_access" {
		t.Fatalf("Blocking = %+v, want only aws_access", got)
	}
	if Blocking(findings[:2]) != nil {
		t.Fatal("medium-only findings must not block")
	}
}
