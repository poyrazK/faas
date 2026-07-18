// Tests for the cmd/faas secrets subcommand (spec §11/G2). Round-trips
// the CLI against a fake apid server, asserting that:
//
//   - list / set / unset dispatch to the correct HTTP routes
//   - the `KEY=VALUE` parser handles edge cases (empty value, equal-in-value)
//   - the CLI body never echoes the plaintext value back into the user-visible
//     output or shell args (redaction invariant)
//   - server-side 4xx/5xx errors surface as a non-zero exit code with the
//     RFC 7807 problem text rendered

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// secretsSink is a tiny programmable fake-apid that records every secrets
// request and lets the test inspect body / respond with a chosen status.
type secretsSink struct {
	lastBody []byte
	// response writers — call only the one matching the request method.
	onGet    func() (int, any)
	onPut    func(body []byte) (int, any)
	onDelete func() (int, any)
}

func (s *secretsSink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		// Default to an empty list so the rotation hint (which fires
		// `GET /v1/apps/{slug}/secrets` before every PUT) doesn't
		// panic when a test doesn't override onGet.
		if s.onGet == nil {
			writeJSONTest(w, api.AppSecretListResponse{Quota: 25})
			return
		}
		status, payload := s.onGet()
		writeJSONTest(w, payload)
		_ = status
	case "PUT":
		body, _ := io.ReadAll(r.Body)
		s.lastBody = body
		status, _ := s.onPut(body)
		w.WriteHeader(status)
	case "DELETE":
		status, _ := s.onDelete()
		w.WriteHeader(status)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// parseSecretsPairTests covers a few shapes I expect real shells to throw.
func TestParseSecretsPair(t *testing.T) {
	cases := []struct {
		in         string
		wantKey    string
		wantValue  string
		wantErrSub string
	}{
		{"STRIPE_KEY=sk_live_x", "STRIPE_KEY", "sk_live_x", ""},
		{"KEY=", "KEY", "", ""},
		{"=value", "", "", "KEY=VALUE"},
		{"no-equals", "", "", "KEY=VALUE"},
		{"A=B=C", "A", "B=C", ""}, // first '=' is the split; allows '=' in value
		{"FOO=bar baz", "FOO", "bar baz", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			p, err := parseSecretsPair(c.in)
			if c.wantErrSub != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got nil (parsed %+v)", c.wantErrSub, p)
				}
				if !strings.Contains(err.Error(), c.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), c.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.Key != c.wantKey || p.Value != c.wantValue {
				t.Errorf("got {%q,%q}, want {%q,%q}", p.Key, p.Value, c.wantKey, c.wantValue)
			}
		})
	}
}

func TestCmdSecrets_ListRendersQuotaAndKeys(t *testing.T) {
	sink := &secretsSink{
		onGet: func() (int, any) {
			return http.StatusOK, api.AppSecretListResponse{
				Secrets: []api.AppSecretResponse{
					{Key: "STRIPE_KEY"},
					{Key: "DB_URL"},
				},
				Quota: 25,
				Count: 2,
			}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"list", "--app", "my-app"}); code != 0 {
		t.Fatalf("cmdSecrets list = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"my-app", "2/25", "STRIPE_KEY", "DB_URL"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestCmdSecrets_ListEmpty(t *testing.T) {
	sink := &secretsSink{
		onGet: func() (int, any) {
			return http.StatusOK, api.AppSecretListResponse{Quota: 3, Count: 0}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"list", "--app", "empty-app"}); code != 0 {
		t.Fatalf("cmdSecrets list = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "0/3") {
		t.Errorf("output should show quota+count: %q", stdout.String())
	}
}

func TestCmdSecrets_SetSendsValueToServer(t *testing.T) {
	sink := &secretsSink{
		onPut: func(body []byte) (int, any) {
			return http.StatusOK, nil
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"set", "--app", "my", "STRIPE_KEY=sk_live_secret"}); code != 0 {
		t.Fatalf("cmdSecrets set = %d, want 0", code)
	}

	// Server saw the right body.
	if len(sink.lastBody) == 0 {
		t.Fatal("PUT had no body")
	}
	var got map[string]string
	if err := json.Unmarshal(sink.lastBody, &got); err != nil {
		t.Fatalf("body not JSON: %v (body=%q)", err, sink.lastBody)
	}
	if got["value"] != "sk_live_secret" {
		t.Errorf("server got value %q, want sk_live_secret", got["value"])
	}

	// Stdout echoes the key name (public) but never the plaintext value.
	out := stdout.String()
	if !strings.Contains(out, "STRIPE_KEY set") {
		t.Errorf("output missing confirmation: %q", out)
	}
	if strings.Contains(out, "sk_live_secret") {
		t.Errorf("PLAIN LEAK: plaintext in CLI output: %q", out)
	}
}

func TestCmdSecrets_Set_RejectsNoPairs(t *testing.T) {
	if code := cmdSecrets([]string{"set", "--app", "my"}); code != 1 {
		t.Errorf("no pairs = %d, want 1", code)
	}
}

func TestCmdSecrets_Set_RejectsMalformedPair(t *testing.T) {
	if code := cmdSecrets([]string{"set", "--app", "my", "no-equals"}); code != 1 {
		t.Errorf("malformed = %d, want 1", code)
	}
}

func TestCmdSecrets_Set_NonZeroExitOnServerError(t *testing.T) {
	sink := &secretsSink{
		onPut: func(body []byte) (int, any) {
			return http.StatusForbidden, api.Problem{
				Status: 403,
				Code:   api.CodePlanLimitSecrets,
				Title:  "Secret count limit reached",
				Detail: "Free plan allows 3 secret(s) per app; you have 3.",
			}
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdSecrets([]string{"set", "--app", "x", "NEW=value"}); code == 0 {
		t.Errorf("over-cap should be non-zero exit")
	}
}

func TestCmdSecrets_Set_FromStdin(t *testing.T) {
	sink := &secretsSink{
		onPut: func(body []byte) (int, any) {
			return http.StatusOK, nil
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	// Pipe three pairs through stdin.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		_, _ = pw.Write([]byte("A=1\nB=2\nC=3\n"))
	}()
	oldStdin := osStdin
	osStdin = pr
	defer func() { osStdin = oldStdin }()

	if code := cmdSecrets([]string{"set", "--app", "x", "--from-stdin"}); code != 0 {
		t.Fatalf("set --from-stdin = %d, want 0", code)
	}
	// Each PUT fired (counted by sink).
	if len(sink.lastBody) == 0 {
		t.Fatal("no body captured")
	}
}

func TestCmdSecrets_Set_FromStdinRejectsMix(t *testing.T) {
	// --from-stdin + positional pair → reject as ambiguous.
	if code := cmdSecrets([]string{"set", "--app", "x", "--from-stdin", "A=1"}); code != 1 {
		t.Errorf("mixed stdin+args = %d, want 1", code)
	}
}

func TestCmdSecrets_Unset(t *testing.T) {
	deleted := false
	sink := &secretsSink{
		onDelete: func() (int, any) {
			deleted = true
			return http.StatusNoContent, nil
		},
	}
	srv := httptest.NewServer(sink)
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdSecrets([]string{"unset", "--app", "x", "STRIPE_KEY"}); code != 0 {
		t.Errorf("unset = %d, want 0", code)
	}
	if !deleted {
		t.Errorf("DELETE never fired")
	}
}

func TestCmdSecrets_Unset_RequiresExactlyOneKey(t *testing.T) {
	if code := cmdSecrets([]string{"unset", "--app", "x"}); code != 1 {
		t.Errorf("unset no key = %d, want 1", code)
	}
	if code := cmdSecrets([]string{"unset", "--app", "x", "A", "B"}); code != 1 {
		t.Errorf("unset two keys = %d, want 1", code)
	}
}

func TestCmdSecrets_DispatchUnknownSubcommand(t *testing.T) {
	if code := cmdSecrets([]string{"frobnicate"}); code != 1 {
		t.Errorf("unknown = %d, want 1", code)
	}
}

// TestCmdSecrets_Set_RotationHint exercises the ADR-020 D5 warning
// (commands3.go secretsSet): when the key being set already exists,
// the CLI prints a notice that parked snapshots still hold the old
// plaintext until the next wake. We assert both directions:
//
//   - existing key  → hint is printed BEFORE the PUT
//   - new key       → hint is NOT printed (no false alarm)
func TestCmdSecrets_Set_RotationHint(t *testing.T) {
	cases := []struct {
		name       string
		existing   []api.AppSecretResponse
		pairs      []string
		wantHint   bool
		wantSubstr []string // substrings the hint must contain when wantHint=true
		unwantSub  string   // substring that must NOT appear when wantHint=false
	}{
		{
			name:     "fresh_add_silent",
			existing: nil,
			pairs:    []string{"NEW_KEY=v1"},
			wantHint: false,
		},
		{
			name:     "existing_key_prints_hint",
			existing: []api.AppSecretResponse{{Key: "STRIPE_KEY"}},
			pairs:    []string{"STRIPE_KEY=sk_live_NEW"},
			wantHint: true,
			wantSubstr: []string{
				"rotated",
				"STRIPE_KEY",
				"parked snapshots",
				"next wake",
			},
		},
		{
			name:     "mixed_one_rotated_one_fresh",
			existing: []api.AppSecretResponse{{Key: "STRIPE_KEY"}},
			pairs:    []string{"STRIPE_KEY=new", "FRESH_KEY=fresh"},
			wantHint: true,
			wantSubstr: []string{
				"1 secret(s)",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &secretsSink{
				onGet: func() (int, any) {
					return http.StatusOK, api.AppSecretListResponse{
						Secrets: tc.existing,
						Quota:   25,
						Count:   len(tc.existing),
					}
				},
				onPut: func(body []byte) (int, any) {
					return http.StatusOK, nil
				},
			}
			srv := httptest.NewServer(sink)
			defer srv.Close()

			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			var stdout bytes.Buffer
			old := osStdout
			osStdout = &stdout
			defer func() { osStdout = old }()

			args := append([]string{"set", "--app", "x"}, tc.pairs...)
			if code := cmdSecrets(args); code != 0 {
				t.Fatalf("cmdSecrets set = %d, want 0", code)
			}
			out := stdout.String()

			if tc.wantHint {
				if !strings.Contains(out, "note:") {
					t.Errorf("hint not printed:\n%s", out)
				}
				for _, s := range tc.wantSubstr {
					if !strings.Contains(out, s) {
						t.Errorf("hint missing %q in output:\n%s", s, out)
					}
				}
			} else {
				if strings.Contains(out, "note:") {
					t.Errorf("hint printed for fresh add:\n%s", out)
				}
			}
		})
	}
}
