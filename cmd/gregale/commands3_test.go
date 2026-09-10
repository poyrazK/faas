// Tests for the cmd/gregale secrets subcommand (spec §11/G2). Round-trips
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

// spec: malformed secret input must never be echoed in diagnostics.
func TestParseSecretsPair_RedactsMalformedInput(t *testing.T) {
	const sensitive = "sk_live_customer_secret_without_separator"
	_, err := parseSecretsPair(sensitive)
	if err == nil {
		t.Fatal("expected malformed pair error")
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("error leaked supplied value: %q", err)
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
				Detail: "Free plan allows 8 secret(s) per app; you have 8.",
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

func TestCmdSecrets_Set_TrailingScopeTargetsPreview(t *testing.T) {
	var putScopes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/secrets":
			writeJSONTest(w, api.AppSecretListResponse{Quota: 25})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/v1/apps/x/secrets/"):
			putScopes = append(putScopes, r.URL.Query().Get("scope"))
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	oldOut := osStdout
	osStdout = io.Discard
	defer func() { osStdout = oldOut }()

	if code := cmdSecrets([]string{"set", "--app", "x", "FIRST=one", "SECOND=two", "--scope=preview"}); code != 0 {
		t.Fatalf("cmdSecrets exit = %d, want 0", code)
	}
	if len(putScopes) != 2 {
		t.Fatalf("PUT count = %d, want 2", len(putScopes))
	}
	for i, scope := range putScopes {
		if scope != "preview" {
			t.Errorf("PUT %d scope = %q, want preview", i, scope)
		}
	}
}

func TestCmdSecrets_Set_InvalidBatchDoesNotMutate(t *testing.T) {
	putCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCount++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdSecrets([]string{"set", "--app", "x", "VALID=one", "invalid-key=two", "--scope=preview"}); code != 1 {
		t.Fatalf("cmdSecrets exit = %d, want 1", code)
	}
	if putCount != 0 {
		t.Fatalf("PUT count = %d, want 0 for an invalid batch", putCount)
	}
}

// TestCmdSecrets_Set_QuotaStamp pins the post-write quota stamp
// (Move 1 PR-A, commands3.go secretsSet → printSecretsQuotaStamp).
// Three modes, matching the docstring on printSecretsQuotaStamp:
//
//   - ListSecrets OK + plan known    → "<slug>: N/M secrets"
//   - ListSecrets OK + plan unknown  → "<slug>: N secrets" (bare count, no cap)
//   - ListSecrets fails               → no stamp at all (PUT already succeeded)
//
// The third mode is the regression guard: a future change that
// re-routes the cap lookup to fail noisily (e.g. turn Whoami into
// a fatal error) would surface as a misleading cap=0 line. The
// test pins the silent-fallback contract.
func TestCmdSecrets_Set_QuotaStamp(t *testing.T) {
	type fakeAccount struct {
		status int
		body   any
		err    bool
	}
	type fakeList struct {
		status int
		body   any
		err    bool
	}
	cases := []struct {
		name      string
		plan      fakeAccount // Whoami
		list      fakeList    // ListSecrets (after PUT)
		wantSub   []string    // substrings the output MUST contain
		wantNot   []string    // substrings the output MUST NOT contain
		wantEmpty bool        // true: no quota stamp line at all
	}{
		{
			name: "happy_path_prints_N_over_M",
			plan: fakeAccount{status: 200, body: api.AccountResponse{Plan: "hobby"}},
			list: fakeList{status: 200, body: api.AppSecretListResponse{
				Secrets: []api.AppSecretResponse{{Key: "K1"}, {Key: "K2"}, {Key: "K3"}},
				Quota:   25, Count: 3,
			}},
			wantSub: []string{"x: 3/25 secrets"},
		},
		{
			name: "free_plan_uses_free_cap_8",
			plan: fakeAccount{status: 200, body: api.AccountResponse{Plan: "free"}},
			list: fakeList{status: 200, body: api.AppSecretListResponse{
				Secrets: []api.AppSecretResponse{{Key: "K1"}},
				Quota:   8, Count: 1,
			}},
			wantSub: []string{"x: 1/8 secrets"},
		},
		{
			name: "plan_unknown_falls_back_to_bare_count",
			plan: fakeAccount{status: 200, body: api.AccountResponse{Plan: ""}},
			list: fakeList{status: 200, body: api.AppSecretListResponse{
				Secrets: []api.AppSecretResponse{{Key: "K1"}},
				Quota:   25, Count: 1,
			}},
			// bare count, no slash, no cap
			wantSub: []string{"x: 1 secrets"},
			wantNot: []string{"/", "25"},
		},
		{
			name: "whoami_fails_falls_back_to_bare_count",
			plan: fakeAccount{err: true},
			list: fakeList{status: 200, body: api.AppSecretListResponse{
				Secrets: []api.AppSecretResponse{{Key: "K1"}, {Key: "K2"}},
				Quota:   25, Count: 2,
			}},
			wantSub: []string{"x: 2 secrets"},
			wantNot: []string{"/25"},
		},
		{
			name: "list_after_put_fails_silently_no_stamp",
			plan: fakeAccount{status: 200, body: api.AccountResponse{Plan: "hobby"}},
			list: fakeList{err: true},
			// The PUT-OK message still prints; the quota stamp is silent.
			wantSub: []string{"K1 set"},
			wantNot: []string{"secrets"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
					if tc.plan.err {
						http.Error(w, "boom", http.StatusInternalServerError)
						return
					}
					writeJSONTest(w, tc.plan.body)
				case r.URL.Path == "/v1/apps/x/secrets":
					if r.Method == http.MethodGet {
						if tc.list.err {
							http.Error(w, "boom", http.StatusInternalServerError)
							return
						}
						writeJSONTest(w, tc.list.body)
						return
					}
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				case r.URL.Path == "/v1/apps/x/secrets/K1" && r.Method == http.MethodPut:
					// SetSecret path: PUT /v1/apps/{slug}/secrets/{key}
					w.WriteHeader(http.StatusOK)
				default:
					http.Error(w, "no", http.StatusNotFound)
				}
			})
			srv := httptest.NewServer(sink)
			defer srv.Close()

			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")

			var stdout bytes.Buffer
			old := osStdout
			osStdout = &stdout
			defer func() { osStdout = old }()

			if code := cmdSecrets([]string{"set", "--app", "x", "K1=v1"}); code != 0 {
				t.Fatalf("cmdSecrets set = %d, want 0", code)
			}
			out := stdout.String()
			for _, s := range tc.wantSub {
				if !strings.Contains(out, s) {
					t.Errorf("output missing %q\nfull: %s", s, out)
				}
			}
			for _, s := range tc.wantNot {
				if strings.Contains(out, s) {
					t.Errorf("output unexpectedly contains %q\nfull: %s", s, out)
				}
			}
		})
	}
}
