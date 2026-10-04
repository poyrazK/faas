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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSecretRuntimeReloadLabelSummarizesReportsWithoutClaimingConvergence(t *testing.T) {
	got := secretRuntimeReloadLabel(2, 1, "failed", "not_attempted", "instance-old", []api.SecretRuntimeReloadObservation{
		{InstanceID: "instance-current", Version: 2, Projection: "updated", Signal: "sent"},
		{InstanceID: "instance-old", Version: 1, Projection: "failed", Signal: "not_attempted"},
	})
	for _, want := range []string{"2 active reports", "1 current", "1 stale", "instance-old"} {
		if !strings.Contains(got, want) {
			t.Errorf("runtime summary %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "/2") || strings.Contains(got, "converged") {
		t.Errorf("runtime summary overclaims fleet convergence: %q", got)
	}
}

func TestSecretRuntimeReloadLabelSeparatesApplicationAcknowledgement(t *testing.T) {
	got := secretRuntimeReloadLabel(2, 1, "updated", "sent", "instance-old", []api.SecretRuntimeReloadObservation{
		{InstanceID: "instance-current", Version: 2, Projection: "updated", Signal: "sent", ApplicationAckVersion: 2, ApplicationAck: "applied"},
		{InstanceID: "instance-old", Version: 1, Projection: "updated", Signal: "sent", ApplicationAckVersion: 1, ApplicationAck: "failed"},
	})
	for _, want := range []string{"app ack: 1 applied, 0 failed, 1 stale", "instance-old"} {
		if !strings.Contains(got, want) {
			t.Errorf("runtime summary %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "app applied") {
		t.Errorf("runtime summary should distinguish the app acknowledgement from guest status: %q", got)
	}
}

func TestSetProjectDeploySecrets(t *testing.T) {
	var paths []string
	var bodies []api.PutAppSecretRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		paths = append(paths, r.URL.Path)
		var body api.PutAppSecretRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode secret request: %v", err)
			return
		}
		bodies = append(bodies, body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "fp_test")
	workloads := []api.PlanWorkload{
		{Name: "api"},
		{Name: "worker"},
		{Name: "API"}, // merged detector output must not cause a duplicate write
		{Name: ""},
	}
	pairs := []secretsPair{{Key: "DATABASE_URL", Value: "postgres://user:password@example/db"}}
	configured, err := setProjectDeploySecrets(context.Background(), client, workloads, pairs)
	if err != nil {
		t.Fatalf("setProjectDeploySecrets() error = %v", err)
	}
	if configured != 2 {
		t.Fatalf("configured = %d, want 2", configured)
	}
	wantPaths := []string{"/v1/apps/api/secrets/DATABASE_URL", "/v1/apps/worker/secrets/DATABASE_URL"}
	if len(paths) != len(wantPaths) {
		t.Fatalf("got %d secret writes, want %d (%v)", len(paths), len(wantPaths), paths)
	}
	for i, want := range wantPaths {
		if paths[i] != want {
			t.Errorf("write %d path = %q, want %q", i, paths[i], want)
		}
		if bodies[i].Value != pairs[0].Value {
			t.Errorf("write %d value = %q, want original value", i, bodies[i].Value)
		}
	}
}

func TestSetDeploySecretsWithScope(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "fp_test")
	err := setDeploySecretsWithScope(context.Background(), client, "api",
		[]secretsPair{{Key: "DATABASE_URL", Value: "postgres://example"}}, "staging")
	if err != nil {
		t.Fatalf("setDeploySecretsWithScope() error = %v", err)
	}
	if query != "scope=staging" {
		t.Fatalf("secret write query = %q, want scope=staging", query)
	}
}

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
					{Key: "STRIPE_KEY", SecretClass: api.SecretClassEphemeral, DeliveryVersion: 2, DeliveryStatus: "pending", LastRuntimeReloadVersion: 2, LastRuntimeReloadProjection: "updated", LastRuntimeReloadSignal: "sent", LastRuntimeReloadInstanceID: "instance-1"},
					{Key: "DB_URL", SecretClass: api.SecretClassPersistent, DeliveryVersion: 3, DeliveryStatus: "delivered", LastRuntimeReloadVersion: 1, LastRuntimeReloadProjection: "updated", LastRuntimeReloadSignal: "sent", LastRuntimeReloadInstanceID: "instance-2"},
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
	for _, want := range []string{"my-app", "2/25", "STRIPE_KEY", "ephemeral", "delivery pending", "runtime file updated; signal sent (instance-1)", "DB_URL", "persistent", "delivery delivered", "runtime status stale (v1) (instance-2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestCmdSecrets_ListFiltersByClass(t *testing.T) {
	sink := &secretsSink{onGet: func() (int, any) {
		return http.StatusOK, api.AppSecretListResponse{
			Secrets: []api.AppSecretResponse{
				{Key: "EPHEMERAL_KEY", SecretClass: api.SecretClassEphemeral},
				{Key: "PERSISTENT_KEY", SecretClass: api.SecretClassPersistent},
				{Key: "LEGACY_KEY"}, // Missing class from an older server defaults to persistent.
			},
			Quota: 25,
			Count: 3,
		}
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"list", "--app", "my-app", "--class", "persistent"}); code != 0 {
		t.Fatalf("cmdSecrets list = %d, want 0", code)
	}
	out := stdout.String()
	for _, want := range []string{"2/25 secrets", "PERSISTENT_KEY", "LEGACY_KEY", "persistent"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "EPHEMERAL_KEY") {
		t.Errorf("filtered output includes ephemeral secret:\n%s", out)
	}
}

func TestCmdSecrets_ListFiltersByAgeAndShowsUpdateTime(t *testing.T) {
	resetJSONOut(t)
	now := time.Now().UTC()
	oldUpdated := now.Add(-120 * 24 * time.Hour).Format(time.RFC3339)
	recentUpdated := now.Add(-24 * time.Hour).Format(time.RFC3339)
	sink := &secretsSink{onGet: func() (int, any) {
		return http.StatusOK, api.AppSecretListResponse{
			Secrets: []api.AppSecretResponse{
				{Key: "OLD_KEY", UpdatedAt: oldUpdated},
				{Key: "RECENT_KEY", UpdatedAt: recentUpdated},
				{Key: "UNKNOWN_KEY"},
			},
			Quota: 25,
			Count: 3,
		}
	}}
	srv := httptest.NewServer(sink)
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"list", "--app", "my-app", "--older-than", "90d"}); code != 0 {
		t.Fatalf("cmdSecrets list = %d, want 0", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "1/25 secrets") || !strings.Contains(out, "OLD_KEY") || !strings.Contains(out, "updated "+oldUpdated) {
		t.Errorf("stale listing should show the matching key and timestamp: %q", out)
	}
	for _, excluded := range []string{"RECENT_KEY", "UNKNOWN_KEY"} {
		if strings.Contains(out, excluded) {
			t.Errorf("stale listing unexpectedly includes %s: %q", excluded, out)
		}
	}
}

func TestFilterAppSecretListByClassPreservesNestedShape(t *testing.T) {
	resp := api.AppSecretListResponse{
		SecretsByScope: api.SecretByScope{
			"prod": {
				{Key: "SESSION", SecretClass: api.SecretClassEphemeral},
				{Key: "DATABASE", SecretClass: api.SecretClassPersistent},
			},
			"staging": {{Key: "OLD_KEY"}},
			"dev":     {{Key: "TEST_TOKEN", SecretClass: api.SecretClassEphemeral}},
		},
		Count: 4,
		Quota: 25,
	}
	filterAppSecretListByClass(&resp, api.SecretClassPersistent)
	if resp.Count != 2 || len(resp.SecretsByScope) != 3 || len(resp.SecretsByScope["prod"]) != 1 || len(resp.SecretsByScope["staging"]) != 1 || len(resp.SecretsByScope["dev"]) != 0 {
		t.Fatalf("filtered nested response = %+v", resp)
	}
	if resp.SecretsByScope["prod"][0].Key != "DATABASE" || resp.SecretsByScope["staging"][0].Key != "OLD_KEY" {
		t.Fatalf("filtered rows = %+v", resp.SecretsByScope)
	}
	var rendered bytes.Buffer
	renderSecretsByScope(&rendered, "demo", &resp)
	if !strings.Contains(rendered.String(), "across 2 scopes") {
		t.Errorf("filtered scope count includes an empty scope: %q", rendered.String())
	}
}

func TestValidSecretClassFilterValues(t *testing.T) {
	for _, class := range []string{"", api.SecretClassPersistent, api.SecretClassEphemeral} {
		if !validSecretClass(class) {
			t.Errorf("validSecretClass(%q) = false", class)
		}
	}
	if validSecretClass("temporary") {
		t.Fatal("validSecretClass accepted an unknown retention class")
	}
}

func TestParseSecretAge(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
		bad   bool
	}{
		{"", 0, false},
		{"90d", 90 * 24 * time.Hour, false},
		{"1h30m", 90 * time.Minute, false},
		{"0d", 0, true},
		{"-1d", 0, true},
		{"1.5d", 0, true},
		{"not-a-duration", 0, true},
		{"999999999999999999d", 0, true},
	} {
		got, err := parseSecretAge(tc.value)
		if tc.bad {
			if err == nil {
				t.Errorf("parseSecretAge(%q) = %v, want error", tc.value, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("parseSecretAge(%q) = %v, %v; want %v", tc.value, got, err, tc.want)
		}
	}
}

func TestFilterAppSecretListByAgeHandlesFlatAndNestedResponses(t *testing.T) {
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	resp := api.AppSecretListResponse{
		Secrets: []api.AppSecretResponse{
			{Key: "OLD", UpdatedAt: "2026-08-01T00:00:00Z"},
			{Key: "BOUNDARY", UpdatedAt: cutoff.Format(time.RFC3339)},
			{Key: "NEW", UpdatedAt: "2026-09-02T00:00:00Z"},
			{Key: "UNKNOWN"},
			{Key: "INVALID", UpdatedAt: "not-a-timestamp"},
		},
		Count: 5,
	}
	filterAppSecretListByAge(&resp, cutoff)
	if resp.Count != 2 || len(resp.Secrets) != 2 || resp.Secrets[0].Key != "OLD" || resp.Secrets[1].Key != "BOUNDARY" {
		t.Fatalf("flat age-filtered response = %+v", resp)
	}

	nested := api.AppSecretListResponse{SecretsByScope: api.SecretByScope{
		"prod":    {{Key: "OLD_PROD", UpdatedAt: "2026-08-01T00:00:00Z"}, {Key: "NEW_PROD", UpdatedAt: "2026-09-02T00:00:00Z"}},
		"staging": {{Key: "UNKNOWN"}},
	}, Count: 3}
	filterAppSecretListByAge(&nested, cutoff)
	if nested.Count != 1 || len(nested.SecretsByScope["prod"]) != 1 || nested.SecretsByScope["prod"][0].Key != "OLD_PROD" || len(nested.SecretsByScope["staging"]) != 0 {
		t.Fatalf("nested age-filtered response = %+v", nested)
	}
}

func TestFilterAccountSecretListByAgeExcludesUnknownAndKeepsCursor(t *testing.T) {
	resp := api.ListSecretsForAccountResponse{
		Secrets: []api.AccountAppSecretResponse{
			{Key: "OLD", UpdatedAt: "2026-08-01T00:00:00Z"},
			{Key: "NEW", UpdatedAt: "2026-09-02T00:00:00Z"},
			{Key: "UNKNOWN"},
		},
		NextBefore: "demo|UNKNOWN",
	}
	filterAccountSecretListByAge(&resp, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))
	if len(resp.Secrets) != 1 || resp.Secrets[0].Key != "OLD" || resp.NextBefore != "demo|UNKNOWN" {
		t.Fatalf("account age-filtered response = %+v", resp)
	}
}

func TestSecretUpdatedAtSuffix(t *testing.T) {
	if got := secretUpdatedAtSuffix(""); got != "" {
		t.Fatalf("empty timestamp suffix = %q, want empty", got)
	}
	if got := secretUpdatedAtSuffix("2026-08-01T00:00:00Z"); got != " · updated 2026-08-01T00:00:00Z" {
		t.Fatalf("timestamp suffix = %q", got)
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

// `secrets unset` used to have no --restart, so a removed secret stayed in
// every running instance's environment until the next cold wake.
func TestCmdSecretsUnsetRestartUsesFreshRestartAfterDelete(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/secrets/"):
			calls = append(calls, "delete")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/restart":
			calls = append(calls, "restart:"+r.URL.Query().Get("fresh"))
			writeJSONTestStatus(w, http.StatusAccepted, api.AppRestartResponse{WakeID: "wake-unset-1"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"unset", "--app", "x", "OLD_KEY", "--restart"}); code != 0 {
		t.Fatalf("cmdSecrets unset --restart = %d, want 0", code)
	}
	if len(calls) != 2 || calls[0] != "delete" || calls[1] != "restart:true" {
		t.Fatalf("calls = %v, want [delete restart:true]", calls)
	}
	if !strings.Contains(stdout.String(), "wake-unset-1") || strings.Contains(stdout.String(), "next cold wake") {
		t.Fatalf("restart output = %q", stdout.String())
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

func TestCmdSecretsSetExplainsDefaultNextColdWake(t *testing.T) {
	sink := &secretsSink{
		onGet: func() (int, any) { return http.StatusOK, api.AppSecretListResponse{Quota: 25, Count: 1} },
		onPut: func([]byte) (int, any) { return http.StatusOK, nil },
	}
	server := httptest.NewServer(sink)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"set", "--app", "x", "API_TOKEN=v1"}); code != 0 {
		t.Fatalf("cmdSecrets set = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "next cold wake") || !strings.Contains(stdout.String(), "--restart") {
		t.Fatalf("default output lacks apply semantics: %q", stdout.String())
	}
}

func TestCmdSecretsSetSendsEphemeralClass(t *testing.T) {
	var body api.PutAppSecretRequest
	sink := &secretsSink{
		onPut: func(raw []byte) (int, any) {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("decode secret request: %v", err)
			}
			return http.StatusOK, nil
		},
	}
	server := httptest.NewServer(sink)
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdSecrets([]string{"set", "--app", "x", "SESSION_TOKEN=value", "--class", api.SecretClassEphemeral}); code != 0 {
		t.Fatalf("cmdSecrets set ephemeral = %d, want 0", code)
	}
	if body.Value != "value" || body.SecretClass != api.SecretClassEphemeral {
		t.Fatalf("secret request = %+v", body)
	}

	if code := cmdSecrets([]string{"set", "--app", "x", "SESSION_TOKEN=value", "--class", "temporary"}); code != 1 {
		t.Fatalf("invalid --class exit = %d, want 1", code)
	}
}

func TestCmdSecretsSetRestartUsesFreshRestartAfterWrites(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/secrets/"):
			calls = append(calls, "put")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/secrets":
			writeJSONTest(w, api.AppSecretListResponse{Quota: 25, Count: 1})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/account":
			writeJSONTest(w, api.AccountResponse{Plan: string(api.PlanHobby)})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/restart":
			calls = append(calls, "restart:"+r.URL.Query().Get("fresh"))
			writeJSONTestStatus(w, http.StatusAccepted, api.AppRestartResponse{WakeID: "wake-secret-1"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"set", "--app", "x", "API_TOKEN=v2", "--restart"}); code != 0 {
		t.Fatalf("cmdSecrets set --restart = %d, want 0", code)
	}
	if len(calls) != 2 || calls[0] != "put" || calls[1] != "restart:true" {
		t.Fatalf("calls = %v, want [put restart:true]", calls)
	}
	if !strings.Contains(stdout.String(), "wake-secret-1") || strings.Contains(stdout.String(), "next cold wake") {
		t.Fatalf("restart output = %q", stdout.String())
	}
}

func TestCmdSecretsRotateRestartUsesFreshRestart(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/secrets/API_TOKEN/rotate":
			calls = append(calls, "rotate")
			writeJSONTest(w, api.RotateAppSecretResponse{
				Key: "API_TOKEN", RotatedAt: "2026-09-22T12:00:00Z", Kid: "age1examplekey",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/restart":
			calls = append(calls, "restart:"+r.URL.Query().Get("fresh"))
			writeJSONTestStatus(w, http.StatusAccepted, api.AppRestartResponse{WakeID: "wake-rotate-1"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"rotate", "--app", "x", "API_TOKEN=v2", "--restart"}); code != 0 {
		t.Fatalf("cmdSecrets rotate --restart = %d, want 0", code)
	}
	if len(calls) != 2 || calls[0] != "rotate" || calls[1] != "restart:true" {
		t.Fatalf("calls = %v, want [rotate restart:true]", calls)
	}
	if !strings.Contains(stdout.String(), "wake-rotate-1") {
		t.Fatalf("restart output = %q", stdout.String())
	}
}

func TestCmdSecretsRotateWaitForAckAfterKeyPair(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/secrets/DATABASE_URL/rotate":
			calls = append(calls, "rotate")
			writeJSONTest(w, api.RotateAppSecretResponse{Key: "DATABASE_URL", RotatedAt: "2026-09-26T12:00:00Z"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/secrets":
			calls = append(calls, "status")
			writeJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{
				Key: "DATABASE_URL", Scope: api.DefaultEnvScope, DeliveryVersion: 2, RuntimeReloadTargetsComplete: true,
				RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
					InstanceID: "instance-1", ReloadSupport: "enabled", Reported: true, Version: 2,
					ApplicationAckVersion: 2, ApplicationAck: "applied",
				}},
			}}})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"rotate", "--app", "x", "DATABASE_URL=v2", "--wait-for-ack", "--timeout", "1s"}); code != 0 {
		t.Fatalf("cmdSecrets rotate --wait-for-ack = %d, want 0", code)
	}
	if len(calls) != 2 || calls[0] != "rotate" || calls[1] != "status" {
		t.Fatalf("calls = %v, want [rotate status]", calls)
	}
	if !strings.Contains(stdout.String(), "All 1 active authorized runtime(s) confirmed") {
		t.Fatalf("acknowledgement output = %q", stdout.String())
	}
}

func TestCmdSecretsRotateRestartWaitsForColdStartAcknowledgement(t *testing.T) {
	var calls []string
	const wakeID = "wake-secret-restart-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/secrets/DATABASE_URL/rotate":
			calls = append(calls, "rotate")
			writeJSONTest(w, api.RotateAppSecretResponse{Key: "DATABASE_URL", RotatedAt: "2026-09-26T12:00:00Z"})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/x/restart":
			calls = append(calls, "restart")
			if r.URL.Query().Get("fresh") != "true" {
				t.Fatalf("restart query = %q, want fresh=true", r.URL.RawQuery)
			}
			writeJSONTestStatus(w, http.StatusAccepted, api.AppRestartResponse{WakeID: wakeID})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/instances":
			calls = append(calls, "wake")
			if r.URL.Query().Get("history") != "true" {
				t.Fatalf("instance history query = %q, want true", r.URL.RawQuery)
			}
			writeJSONTest(w, []api.InstanceResponse{{ID: "instance-1", State: "running", WakeID: wakeID}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/x/secrets":
			calls = append(calls, "status")
			writeJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{
				Key: "DATABASE_URL", Scope: api.DefaultEnvScope, DeliveryVersion: 2, RuntimeReloadTargetsComplete: true,
				RuntimeReloadObservations: []api.SecretRuntimeReloadObservation{{
					InstanceID: "instance-1", ReloadSupport: "disabled", Reported: true,
					ApplicationAckVersion: 2, ApplicationAck: "applied",
				}},
			}}})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	var stdout bytes.Buffer
	old := osStdout
	osStdout = &stdout
	defer func() { osStdout = old }()

	if code := cmdSecrets([]string{"rotate", "--app", "x", "DATABASE_URL=v2", "--restart", "--wait-for-ack", "--timeout", "1s"}); code != 0 {
		t.Fatalf("cmdSecrets rotate --restart --wait-for-ack = %d", code)
	}
	if len(calls) != 4 || calls[0] != "rotate" || calls[1] != "restart" || calls[2] != "wake" || calls[3] != "status" {
		t.Fatalf("calls = %v, want [rotate restart wake status]", calls)
	}
	if !strings.Contains(stdout.String(), "Restart completed") || !strings.Contains(stdout.String(), "All 1 active authorized runtime(s) confirmed") {
		t.Fatalf("restart acknowledgement output = %q", stdout.String())
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
			wantNot: []string{"x:"},
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
