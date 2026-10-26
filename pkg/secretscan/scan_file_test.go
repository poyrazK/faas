// Whitebox tests for ScanFile. Mirrors the existing scan_test.go shape:
// each case names the input bytes + expected Finding slice.
package secretscan

import (
	"crypto/sha512"
	"encoding/base64"
	"strings"
	"testing"
)

// fakeStripeLiveKey is declared in scan_test.go (the canonical fixture);
// we reuse it here for the JSON-quoted-key positive case.

func TestScanFile_Positives(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		contents string
		want     []Finding
	}{
		{
			name:     "pem_armour",
			path:     "keys/server.pem",
			contents: "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n-----END RSA PRIVATE KEY-----\n",
			want: []Finding{
				{File: "keys/server.pem", Line: 1, Provider: "private_key_block", Severity: SeverityHigh, Snippet: "-----B…----"},
			},
		},
		{
			name:     "aws_in_yaml",
			path:     "config/prod.yaml",
			contents: "aws_access_key_id: AKIAIOSFODNN7EXAMPLE\n",
			// KeyHint for aws_access is "aws"; "aws_access_key_id" contains it.
			want: []Finding{
				{File: "config/prod.yaml", Line: 1, Key: "aws_access_key_id", Provider: "aws_access", Severity: SeverityHigh, Snippet: "AKIAIO…MPLE"},
			},
		},
		{
			name:     "github_pat_in_ts",
			path:     "src/index.ts",
			contents: "const GITHUB_TOKEN = \"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789\"\n",
			// KeyHint for github_pat is "github"; "GITHUB_TOKEN" contains it.
			want: []Finding{
				{File: "src/index.ts", Line: 1, Key: "GITHUB_TOKEN", Provider: "github_pat", Severity: SeverityHigh, Snippet: "ghp_aB…6789"},
			},
		},
		{
			name:     "json_quoted_key",
			path:     "config.json",
			contents: `{"STRIPE_SECRET_KEY": "` + fakeStripeLiveKey + `"}` + "\n",
			// The leading/trailing quotes are stripped from the key.
			// Snippet policy matches the env path: first 6 + ellipsis + last 4.
			want: []Finding{
				{File: "config.json", Line: 1, Key: "STRIPE_SECRET_KEY", Provider: "stripe_live", Severity: SeverityHigh, Snippet: "sk_liv…XXXX"},
			},
		},
		{
			name:     "clean_python",
			path:     "main.py",
			contents: "import os\n\ndef hello():\n    print('hello world')\n",
			want:     nil,
		},
		{
			name:     "comment_line_with_looking_key",
			path:     "notes.md",
			contents: "# STRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n",
			// Comments are skipped, not scanned.
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanFile(tc.path, []byte(tc.contents))
			if len(got) != len(tc.want) {
				t.Fatalf("ScanFile(%q) returned %d findings, want %d: %+v", tc.contents, len(got), len(tc.want), got)
			}
			for i, f := range got {
				if f != tc.want[i] {
					t.Errorf("finding[%d] = %+v, want %+v", i, f, tc.want[i])
				}
			}
		})
	}
}

func TestScanFile_LineNumbers(t *testing.T) {
	contents := "import os\n\n# blank line follows\nconst K = \"ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789\"\n"
	got := ScanFile("main.go", []byte(contents))
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d: %+v", len(got), got)
	}
	if got[0].Line != 4 {
		t.Errorf("Line = %d, want 4 (import at 1, blank at 2, comment at 3, secret at 4)", got[0].Line)
	}
}

// TestScanFile_PEMDedup is the regression pin for review finding #3:
// a 100-line committed PEM block must produce ONE finding (the
// BEGIN-line private_key_block), NOT 100+ findings (one BEGIN + ~100
// high_entropy on the base64 body). Without the dedup gate, --secret-
// scan=strict floods the customer with one warning per base64 line
// for what is logically a single secret.
func TestScanFile_PEMDedup(t *testing.T) {
	// Build a 50-line PEM block by repeating a long random-looking
	// base64 string. Each body line is well over entropyMinLen (20
	// bytes) and well over the 4.5 bits/char entropy floor, so the
	// pre-dedup code would have produced ~50 high_entropy findings
	// + 1 private_key_block = ~51 findings.
	var body strings.Builder
	body.WriteString("-----BEGIN RSA PRIVATE KEY-----\n")
	for i := 0; i < 50; i++ {
		// 80-char random-looking base64; uses a non-secret token
		// (the alphabet 'A'..'z' mixes with digits to keep entropy
		// high enough to trip the floor). Distinct on each line
		// would be required for a real key, but the dedup gate
		// doesn't care about uniqueness — it only cares that the
		// line is INSIDE the BEGIN/END block.
		body.WriteString("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP012\n")
	}
	body.WriteString("-----END RSA PRIVATE KEY-----\n")
	got := ScanFile("keys/server.pem", []byte(body.String()))
	if len(got) != 1 {
		t.Fatalf("PEM block produced %d findings, want 1 (the BEGIN-line private_key_block); full set: %+v", len(got), got)
	}
	if got[0].Provider != "private_key_block" {
		t.Errorf("Provider = %q, want private_key_block", got[0].Provider)
	}
	if got[0].Line != 1 {
		t.Errorf("Line = %d, want 1 (the BEGIN line)", got[0].Line)
	}
}

// A committed CA bundle is public: its base64 body must not read as
// high_entropy secrets, while a key or provider token beside it still does.
func TestScanFile_CertificateBlocksArePublic(t *testing.T) {
	body := strings.Repeat("MIIBPjCB5qADAgECAgEBMAoGCCqGSM49BAMCMA8xDTALBgNVBAMTBGRpYWcwHhcN\n", 6)
	cert := "-----BEGIN CERTIFICATE-----\n" + body + "-----END CERTIFICATE-----\n"
	cases := []struct {
		name, contents string
		want           []string // providers, in order
	}{
		{"certificate bundle", cert + cert, nil},
		{"certificate request", strings.ReplaceAll(cert, "CERTIFICATE", "CERTIFICATE REQUEST"), nil},
		{"key after certificate", cert + "-----BEGIN EC PRIVATE KEY-----\n" + body + "-----END EC PRIVATE KEY-----\n", []string{"private_key_block"}},
		{"body after certificate", cert + strings.SplitAfter(body, "\n")[0], []string{"high_entropy"}},
		{"provider token inside certificate", "-----BEGIN CERTIFICATE-----\nGITHUB_TOKEN=ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789\n-----END CERTIFICATE-----\n", []string{"github_pat"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanFile("runtime/ca.pem", []byte(tc.contents))
			providers := make([]string, 0, len(got))
			for _, f := range got {
				providers = append(providers, f.Provider)
			}
			if strings.Join(providers, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("providers = %v, want %v (%+v)", providers, tc.want, got)
			}
		})
	}
}

func TestScanFile_NoRawValueInSnippet(t *testing.T) {
	// Privacy guard: snippet must never contain the full raw value.
	contents := "STRIPE_SECRET_KEY=" + fakeStripeLiveKey + "\n"
	got := ScanFile(".env", []byte(contents))
	if len(got) != 1 {
		t.Fatalf("want 1 finding, got %d", len(got))
	}
	if got[0].Snippet == fakeStripeLiveKey {
		t.Errorf("Snippet leaked the full raw value: %q", got[0].Snippet)
	}
}

func TestIsKeyShaped(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"STRIPE_SECRET_KEY", true},
		{"_PRIVATE", true},
		{"api-key", true},
		{"stripe.key", true},
		{"123foo", false}, // can't start with digit
		{"foo bar", false},
		{"", false},
		{"foo;rm", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isKeyShaped([]byte(tc.in)); got != tc.want {
				t.Errorf("isKeyShaped(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestScanFile_DegenerateKeysDoNotPanic — unquoteKey's quote check lacked
// parentheses, so its single-quote branch indexed an empty or one-byte key.
// Ordinary source (a Python dict keyed by '=', a continued assignment, a
// `{ :` object literal) panicked the scanner, which runs inside
// `gregale deploy` packaging, apid, and imaged.
func TestScanFile_DegenerateKeysDoNotPanic(t *testing.T) {
	cases := map[string]string{
		"python dict keyed by operator": "OPS = {\n    '=': operator.eq,\n}\n",
		"continued assignment":          "x \\\n    = 1\n",
		"brace then colon":              "{ : 1\n",
		"lone quote then colon":         "':x\n",
		"lone double quote then equals": "\"=x\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("ScanFile panicked: %v", r)
				}
			}()
			_ = ScanFile("ops.py", []byte(src))
		})
	}
}

func TestUnquoteKey(t *testing.T) {
	cases := map[string]string{
		``:        ``,
		`'`:       `'`,
		`"`:       `"`,
		`''`:      ``,
		`"KEY"`:   `KEY`,
		`'KEY'`:   `KEY`,
		`"KEY'`:   `"KEY'`,
		`KEY`:     `KEY`,
		`'KEY`:    `'KEY`,
		`{"KEY"}`: `{"KEY"}`,
	}
	for in, want := range cases {
		if got := string(unquoteKey([]byte(in))); got != want {
			t.Errorf("unquoteKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// H10-9: the durable-entity starter's prepared validator bundle stores its
// sources as base64, which the entropy fallback flagged as secrets.
func TestScanFile_ValidatorBundleScansDecodedSources(t *testing.T) {
	bundle := func(source string) []byte {
		return []byte(`{
  "runtime": "node22",
  "entrypoint": "validator.mjs",
  "files": [
    {
      "path": "validator.mjs",
      "content": "` + base64.StdEncoding.EncodeToString([]byte(source)) + `"
    }
  ]
}
`)
	}
	clean := "// Pure isolated validator: no imports from the SDK, credentials or server code.\nexport default function validateRestore(input) {\n  return { valid: typeof input?.candidate === 'object' };\n}\n"
	if got := ScanFile("gregale.validator.json", bundle(clean)); len(got) != 0 {
		t.Fatalf("clean bundle findings = %+v, want none", got)
	}
	leaky := "const key = \"sk_live_" + "51HbQ2pL8vXk9mT3nR7wY4cZ\";\nexport default function v() { return key; }\n"
	got := ScanFile("app/gregale.validator.json", bundle(leaky))
	if len(got) == 0 {
		t.Fatal("a secret inside a bundled source must still be found")
	}
	if got[0].File != "app/gregale.validator.json:validator.mjs" || got[0].Line != 1 {
		t.Fatalf("finding at %s:%d, want app/gregale.validator.json:validator.mjs:1", got[0].File, got[0].Line)
	}
	// Not a bundle: scanned as ordinary text.
	if got := ScanFile("gregale.validator.json", []byte("token = \"sk_live_"+"51HbQ2pL8vXk9mT3nR7wY4cZ\"\n")); len(got) == 0 {
		t.Fatal("a malformed bundle must fall back to the text scan")
	}
}

// TestScanFile_SRIDigestsArePublic pins ADR-20261026152731: a Subresource
// Integrity attribute (the usual CDN <script> snippet) is a checksum of a
// public file, not a credential.
func TestScanFile_SRIDigestsArePublic(t *testing.T) {
	html := `<script src="https://cdn.example.com/lib.js" integrity="sha384-oqVuAfXRKap7fdgcCY5uykM6+R9GqQ8K/uxy9rx7HNQlGYl1kPzQho1wx4JwY8wC" crossorigin="anonymous"></script>` + "\n"
	if got := ScanFile("templates/base.html", []byte(html)); len(got) != 0 {
		t.Fatalf("SRI attribute produced findings: %+v", got)
	}
	// A literal merely prefixed like a digest, with the wrong length, still scans.
	sum := sha512.Sum512([]byte("not a sha256 digest"))
	bogus := `const token = "sha256-` + base64.StdEncoding.EncodeToString(sum[:40]) + `";` + "\n"
	if got := ScanFile("src/token.js", []byte(bogus)); len(got) != 1 || got[0].Provider != "high_entropy" {
		t.Fatalf("non-digest sha256- literal = %+v, want one high_entropy finding", got)
	}
}

// TestScanFile_AllowSecretMarker pins the per-line escape hatch: the marker
// suppresses every finding on its own line and nothing else.
func TestScanFile_AllowSecretMarker(t *testing.T) {
	body := "STRIPE_KEY = \"" + fakeStripeLiveKey + "\" // " + AllowSecretMarker + "\n" +
		"STRIPE_KEY_2 = \"" + fakeStripeLiveKey + "\"\n"
	got := ScanFile("test/fixtures.js", []byte(body))
	if len(got) != 1 || got[0].Line != 2 || got[0].Provider != "stripe_live" {
		t.Fatalf("findings = %+v, want only line 2 stripe_live", got)
	}
}
