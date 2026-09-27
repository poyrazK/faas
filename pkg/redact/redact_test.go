// Unit tests for pkg/redact. Table-driven; covers every pattern
// in defaultPatterns() with both positive and negative cases plus
// the cap-truncation behaviour.
//
// Patterns covered: email, card, authorization, cookie, x-api-key,
// query_secret, stripe_key, jwt, ipv4.
//
// Security tests (the grep tripwire that confirms NOTHING slipped
// through) live in security_test.go. The red-team corpus lives
// in fixtures_test.go.

package redact

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestRedactor_Apply_Email covers the email pattern: positive,
// negative, and the false-positive guard against "user@host"
// strings that aren't actually email-shaped.
func TestRedactor_Apply_Email(t *testing.T) {
	t.Parallel()
	r := New(512)

	cases := []struct {
		name   string
		input  string
		want   string
		redact []string
	}{
		{
			name:   "simple",
			input:  "user alice@example.com failed",
			want:   "user [REDACTED:email] failed",
			redact: []string{"email"},
		},
		{
			name:   "plus-tag",
			input:  "delivery to alice+tag@sub.example.co.uk bounced",
			want:   "delivery to [REDACTED:email] bounced",
			redact: []string{"email"},
		},
		{
			name:   "no false positive on dotted path",
			input:  `error at obj.field.subfield value`,
			want:   `error at obj.field.subfield value`,
			redact: nil,
		},
		{
			name:   "no false positive on short TLD",
			input:  `version v1.2 of the api`,
			want:   `version v1.2 of the api`,
			redact: nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, names := r.Apply(tc.input)
			if got != tc.want {
				t.Fatalf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if !reflect.DeepEqual(names, tc.redact) {
				t.Fatalf("Apply(%q) redacted %v, want %v", tc.input, names, tc.redact)
			}
		})
	}
}

// TestRedactor_Apply_Card — exactly 13..19 digits with optional
// separators. Confirms UUIDs do NOT trigger (hex letters break the
// digit run) and short digit runs are untouched.
func TestRedactor_Apply_Card(t *testing.T) {
	t.Parallel()
	r := New(512)
	cases := []struct {
		name   string
		input  string
		want   string
		redact []string
	}{
		{
			name:   "16 digit run",
			input:  "charged card 4242424242424242 ok",
			want:   "charged card [REDACTED:card] ok",
			redact: []string{"card"},
		},
		{
			name:   "13 digit visa-electron-ish",
			input:  "card ending 4242424242429",
			want:   "card ending [REDACTED:card]",
			redact: []string{"card"},
		},
		{
			name:   "spaced grouping",
			input:  "card 4242 4242 4242 4242",
			want:   "card [REDACTED:card]",
			redact: []string{"card"},
		},
		{
			name:   "hyphenated grouping",
			input:  "card 4242-4242-4242-4242",
			want:   "card [REDACTED:card]",
			redact: []string{"card"},
		},
		{
			name:   "uuid does not trigger",
			input:  "request 550e8400-e29b-41d4-a716-446655440000 done",
			want:   "request 550e8400-e29b-41d4-a716-446655440000 done",
			redact: nil,
		},
		{
			name:   "short digit run untouched",
			input:  "http 504 timeout after 5000ms",
			want:   "http 504 timeout after 5000ms",
			redact: nil,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, names := r.Apply(tc.input)
			if got != tc.want {
				t.Fatalf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if !reflect.DeepEqual(names, tc.redact) {
				t.Fatalf("Apply(%q) redacted %v, want %v", tc.input, names, tc.redact)
			}
		})
	}
}

// TestRedactor_Apply_AuthorizationHeader — header-style rules.
// Case is preserved (lowercase input → lowercase output header name).
func TestRedactor_Apply_AuthorizationHeader(t *testing.T) {
	t.Parallel()
	r := New(512)
	cases := []struct {
		name   string
		input  string
		want   string
		redact []string
	}{
		{
			name:   "bearer in header",
			input:  "Authorization: Bearer eyJabc.def.ghi",
			want:   "Authorization: [REDACTED:authorization]",
			redact: []string{"authorization"},
		},
		{
			name:   "basic in header preserves lowercase",
			input:  "authorization: Basic dXNlcjpwYXNz",
			want:   "authorization: [REDACTED:authorization]",
			redact: []string{"authorization"},
		},
		{
			name:   "no header wrapping — bare JWT fires the jwt rule",
			input:  "carrying eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.abc123DEF456ghi789 inline",
			want:   "carrying [REDACTED:jwt] inline",
			redact: []string{"jwt"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, names := r.Apply(tc.input)
			if got != tc.want {
				t.Fatalf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if !reflect.DeepEqual(names, tc.redact) {
				t.Fatalf("Apply(%q) redacted %v, want %v", tc.input, names, tc.redact)
			}
		})
	}
}

// TestRedactor_Apply_CookieHeader
func TestRedactor_Apply_CookieHeader(t *testing.T) {
	t.Parallel()
	r := New(512)
	got, names := r.Apply("Cookie: session=abc123; csrf=def456")
	want := "Cookie: [REDACTED:cookie]"
	if got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(names, []string{"cookie"}) {
		t.Fatalf("redacted %v, want [cookie]", names)
	}
}

// TestRedactor_Apply_XAPIKeyHeader — case-preservation for both
// X-Api-Key and x-api-token header names.
func TestRedactor_Apply_XAPIKeyHeader(t *testing.T) {
	t.Parallel()
	r := New(512)
	cases := []struct {
		input string
		want  string
	}{
		{"X-Api-Key: sk_live_xyz", "X-Api-Key: [REDACTED:x-api-key]"},
		{"x-api-token: tok_12345", "x-api-token: [REDACTED:x-api-key]"},
	}
	for _, tc := range cases {
		got, _ := r.Apply(tc.input)
		if got != tc.want {
			t.Errorf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestRedactor_Apply_QuerySecret — query-string secrets are matched
// as the key=value pair; the Replacer rebuilds "<key>=[REDACTED:...]".
func TestRedactor_Apply_QuerySecret(t *testing.T) {
	t.Parallel()
	r := New(512)
	cases := []struct {
		input string
		want  string
	}{
		{"GET /v1?api_key=sk_live_xyz&page=2", "GET /v1?api_key=[REDACTED:query_secret]&page=2"},
		{"https://x/y?token=abc.def&other=ok", "https://x/y?token=[REDACTED:query_secret]&other=ok"},
		{"https://x/y?password=hunter2 ok", "https://x/y?password=[REDACTED:query_secret] ok"},
		{"https://x/y?access_token=eyJabc.def.ghi", "https://x/y?access_token=[REDACTED:query_secret]"},
	}
	for _, tc := range cases {
		got, _ := r.Apply(tc.input)
		if got != tc.want {
			t.Errorf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestRedactor_Apply_StripeKey — covers both live + test prefixes.
func TestRedactor_Apply_StripeKey(t *testing.T) {
	t.Parallel()
	r := New(512)
	cases := []struct {
		input string
		want  string
	}{
		{"stripe.init('sk_live_0000000000000XYZFAKE')", "stripe.init('[REDACTED:stripe_key]')"},
		{"pk_test_0000000000000XYZFAKE", "[REDACTED:stripe_key]"},
		{"rk_live_0000000000000XYZFAKE", "[REDACTED:stripe_key]"},
		{"too short sk_live_abc", "too short sk_live_abc"}, // <16 alnum after prefix — not redacted
	}
	for _, tc := range cases {
		got, _ := r.Apply(tc.input)
		if got != tc.want {
			t.Errorf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestRedactor_Apply_JWT — a bare JWT (no Authorization header
// wrapping) fires the jwt rule.
func TestRedactor_Apply_JWT(t *testing.T) {
	t.Parallel()
	r := New(512)
	got, names := r.Apply("token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1MSJ9.abc123DEF456ghi789 here")
	if !contains(names, "jwt") {
		t.Fatalf("expected jwt redaction, got %v in %q", names, got)
	}
}

// TestRedactor_Apply_IPv4
func TestRedactor_Apply_IPv4(t *testing.T) {
	t.Parallel()
	r := New(512)
	cases := []struct {
		input string
		want  string
	}{
		{"dial tcp 192.168.1.42:5432: connection refused", "dial tcp [REDACTED:ipv4]:5432: connection refused"},
		{"10.0.0.2 is up", "[REDACTED:ipv4] is up"},
		{"v1.2.3.4 is a version, not an IP", "v1.2.3.4 is a version, not an IP"}, // octets >255 — not an IP
		{"255.255.255.255 boundary", "[REDACTED:ipv4] boundary"},
		{"256.1.1.1 overflow", "256.1.1.1 overflow"},
	}
	for _, tc := range cases {
		got, _ := r.Apply(tc.input)
		if got != tc.want {
			t.Errorf("Apply(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestRedactor_Apply_CapTruncation — long inputs are truncated
// to cap bytes + "..." BEFORE regex matching. Verify a long
// input with a secret in the tail loses the tail.
func TestRedactor_Apply_CapTruncation(t *testing.T) {
	t.Parallel()
	r := New(64)

	// 200-char input, cap 64. After truncation we have 64 chars
	// + "..." = 67 chars; the secret ("SECRET") appears at
	// position 100, well past the cut.
	in := strings.Repeat("a", 100) + " SECRET-PASSWORD " + strings.Repeat("b", 50)
	got, names := r.Apply(in)

	if len(got) != 64+len("...") {
		t.Fatalf("expected output len = %d, got %d", 64+len("..."), len(got))
	}
	if strings.Contains(got, "SECRET") {
		t.Fatalf("secret should have been truncated away, got %q", got)
	}
	if !contains(names, "truncated") {
		t.Fatalf("expected truncated marker in %v", names)
	}
}

// TestRedactor_Apply_MultiPII — a single input with multiple PII
// types; verifies that every pattern fires and that the returned
// name list is sorted + unique.
func TestRedactor_Apply_MultiPII(t *testing.T) {
	t.Parallel()
	r := New(1024)
	in := "user alice@example.com card 4242424242424242 from 10.0.0.5"
	got, names := r.Apply(in)

	wantSubstrings := []string{
		"[REDACTED:email]",
		"[REDACTED:card]",
		"[REDACTED:ipv4]",
	}
	for _, w := range wantSubstrings {
		if !strings.Contains(got, w) {
			t.Errorf("Apply(%q) = %q, missing %q", in, got, w)
		}
	}

	wantNames := []string{"card", "email", "ipv4"}
	sort.Strings(wantNames) // already sorted; explicit for clarity
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("Apply(%q) names = %v, want %v", in, names, wantNames)
	}
}

// TestRedactor_Apply_Empty — the empty string round-trips.
func TestRedactor_Apply_Empty(t *testing.T) {
	t.Parallel()
	r := New(512)
	got, names := r.Apply("")
	if got != "" {
		t.Fatalf("Apply(\"\") = %q, want \"\"", got)
	}
	if names != nil {
		t.Fatalf("Apply(\"\") names = %v, want nil", names)
	}
}

// TestRedactor_ApplyHeaders — header map redaction. Verifies the
// map is copied (input not mutated), and the returned names list
// is the union across all values.
func TestRedactor_ApplyHeaders(t *testing.T) {
	t.Parallel()
	r := New(256)
	in := map[string]string{
		"User-Agent":      "curl/8.4.1",
		"Authorization":   "Bearer eyJabc.def.ghi",
		"X-Forwarded-For": "10.0.0.5",
	}
	out, names := r.ApplyHeaders(in)

	// Input must NOT be mutated.
	if in["Authorization"] != "Bearer eyJabc.def.ghi" {
		t.Fatalf("input mutated: %v", in)
	}
	// Output must have the redactions. ApplyHeaders matches each
	// value with its header name in front, so the authorization
	// rule redacts the whole credential by name — not just the
	// JWT-shaped part of it.
	if out["Authorization"] != "[REDACTED:authorization]" {
		t.Fatalf("Authorization redaction missing: %q", out["Authorization"])
	}
	if out["X-Forwarded-For"] != "[REDACTED:ipv4]" {
		t.Fatalf("X-Forwarded-For redaction missing: %q", out["X-Forwarded-For"])
	}
	if out["User-Agent"] != "curl/8.4.1" {
		t.Fatalf("User-Agent should be untouched: %q", out["User-Agent"])
	}
	// Returned names: authorization (caught by header name) + ipv4.
	if !contains(names, "authorization") || !contains(names, "ipv4") {
		t.Fatalf("expected authorization + ipv4 in %v", names)
	}
}

// TestRedactor_ApplyHeaders_CredentialHeadersByName — the gateway feeds
// req.Header in as {name: value}, so a credential header must be redacted
// by its name. Before the fix only JWT-shaped values were caught, and
// opaque bearer tokens, session cookies and API keys were persisted to the
// customer-facing error store verbatim.
func TestRedactor_ApplyHeaders_CredentialHeadersByName(t *testing.T) {
	t.Parallel()
	r := New(256)
	cases := []struct {
		header, value, want, name string
	}{
		{"Authorization", "Bearer ghp_16C7e42F292c6912E7710c838347Ae178B4a", "[REDACTED:authorization]", "authorization"},
		{"authorization", "Basic YWxhZGRpbjpvcGVuc2VzYW1l", "[REDACTED:authorization]", "authorization"},
		{"Proxy-Authorization", "Bearer opaque-token-value", "[REDACTED:authorization]", "authorization"},
		{"Cookie", "session=9f8e7d6c5b4a39281706f5e4d3c2b1a0", "[REDACTED:cookie]", "cookie"},
		{"X-Api-Key", "k_9f8e7d6c5b4a39281706", "[REDACTED:x-api-key]", "x-api-key"},
		{"X-API-Token", "t_0123456789abcdef", "[REDACTED:x-api-key]", "x-api-key"},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			out, names := r.ApplyHeaders(map[string]string{tc.header: tc.value})
			if out[tc.header] != tc.want {
				t.Fatalf("%s = %q, want %q", tc.header, out[tc.header], tc.want)
			}
			if !contains(names, tc.name) {
				t.Fatalf("names = %v, want %q", names, tc.name)
			}
		})
	}
	// Non-credential headers keep their value; the name is never
	// prepended to the output.
	out, names := r.ApplyHeaders(map[string]string{"User-Agent": "curl/8.4.1"})
	if out["User-Agent"] != "curl/8.4.1" || names != nil {
		t.Fatalf("User-Agent = %q names=%v, want untouched", out["User-Agent"], names)
	}
}

// TestRedactor_Apply_TruncationKeepsUTF8 — the cap is a byte cap but the
// output is written to a Postgres text column, which rejects invalid UTF-8
// (SQLSTATE 22021). A multi-byte rune straddling the cap must be dropped
// whole, not split.
func TestRedactor_Apply_TruncationKeepsUTF8(t *testing.T) {
	t.Parallel()
	r := New(512)
	for _, tail := range []string{"é", "€", "😀"} {
		in := "/" + strings.Repeat("a", 510) + tail
		got, names := r.Apply(in)
		if !utf8.ValidString(got) {
			t.Fatalf("tail %q: Apply returned invalid UTF-8: %q", tail, got[len(got)-8:])
		}
		if !strings.HasSuffix(got, "...") || !contains(names, "truncated") {
			t.Fatalf("tail %q: got %q names=%v, want truncation marker", tail, got[len(got)-8:], names)
		}
		if len(got) > 512+len("...") {
			t.Fatalf("tail %q: len %d exceeds cap", tail, len(got))
		}
	}
}

// TestRedactor_ApplyHeaders_Empty — empty input returns empty map + nil.
func TestRedactor_ApplyHeaders_Empty(t *testing.T) {
	t.Parallel()
	r := New(256)
	out, names := r.ApplyHeaders(nil)
	if len(out) != 0 {
		t.Fatalf("expected empty map, got %v", out)
	}
	if names != nil {
		t.Fatalf("expected nil names, got %v", names)
	}
	out, names = r.ApplyHeaders(map[string]string{})
	if len(out) != 0 {
		t.Fatalf("expected empty map, got %v", out)
	}
	if names != nil {
		t.Fatalf("expected nil names, got %v", names)
	}
}

// TestRedactor_Apply_NoCap — cap=0 falls back to 4096. A 4096-char
// input should pass through untouched (no truncation marker); a
// 8000-char input should be truncated.
func TestRedactor_Apply_NoCap(t *testing.T) {
	t.Parallel()
	r := New(0)

	// 4096 chars — at the cap; no truncation.
	in := strings.Repeat("a", 4096)
	got, names := r.Apply(in)
	if len(got) != 4096 {
		t.Fatalf("4096-char input should pass through unchanged; got len %d", len(got))
	}
	if contains(names, "truncated") {
		t.Fatalf("expected no truncation marker at exact cap, got %v", names)
	}

	// 8000 chars — over the cap; truncated.
	in = strings.Repeat("a", 8000)
	got, names = r.Apply(in)
	if len(got) != 4096+len("...") {
		t.Fatalf("8000-char input should truncate to 4099; got len %d", len(got))
	}
	if !contains(names, "truncated") {
		t.Fatalf("expected truncation marker, got %v", names)
	}
}

// TestRedactor_DefaultPatternsExported — Default() returns the
// same set the wire-side grep tripwire consumes.
func TestRedactor_DefaultPatternsExported(t *testing.T) {
	t.Parallel()
	ps := Default()
	if len(ps) < 9 {
		t.Fatalf("expected ≥9 patterns (email, card, authorization, cookie, x-api-key, query_secret, stripe_key, jwt, ipv4), got %d", len(ps))
	}
	wantNames := []string{
		"email", "card", "authorization", "cookie", "x-api-key",
		"query_secret", "stripe_key", "jwt", "ipv4",
	}
	gotNames := make([]string, 0, len(ps))
	for _, p := range ps {
		gotNames = append(gotNames, p.Name)
	}
	for _, want := range wantNames {
		if !contains(gotNames, want) {
			t.Errorf("Default() missing pattern %q", want)
		}
	}
}

// contains — small helper to avoid importing slices just for this.
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
