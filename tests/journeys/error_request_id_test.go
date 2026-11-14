package journeys

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// GA row 156: a rejected read-only query can be correlated without debug mode.
func TestErrorRequestIDLiveAPI(t *testing.T) {
	tg := needs(t, tierSmoke, true)
	t.Parallel()
	r := tg.run(t, time.Minute, "billing", "usage", "summary", "--month", "not-a-month")
	if r.exit == 0 || len(r.stdout) != 0 {
		t.Fatal("invalid month did not fail cleanly")
	}
	var problem struct {
		ID string `json:"request_id"`
	}
	if err := json.Unmarshal(r.stderr, &problem); err != nil || problem.ID == "" {
		t.Fatalf("API error has no correlation ID: %s", r.stderr)
	}
}

func TestErrorRequestIDIsolatedAPI(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	t.Parallel()
	const id = "019a767e7f0070008000000000000001"
	for _, tc := range []struct {
		name, body string
		ids        []string
		want       string
		command    []string
	}{
		{"json", `{"status":503,"code":"service_unavailable","detail":"try later","request_id":"body-private"}`, []string{id}, id, nil},
		{"proxy", `<html>proxy-private</html>`, []string{id}, id, nil},
		{"missing", `{"status":503,"code":"service_unavailable","request_id":"019a767e7f0070008000000000000001"}`, nil, "", nil},
		{"invalid", `{}`, []string{"header-private"}, "", nil},
		{"duplicate", `{}`, []string{id, id}, "", nil},
		{"invocation wait", `{"status":503,"code":"service_unavailable"}`, []string{id}, id, []string{"invocation", "wait", "inv-correlation"}},
		{"invocation wait missing", `{"status":503,"code":"service_unavailable","request_id":"body-private"}`, nil, "", []string{"invocation", "wait", "inv-correlation"}},
		{"invocation wait duplicate", `{"status":503,"code":"service_unavailable"}`, []string{id, id}, "", []string{"invocation", "wait", "inv-correlation"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/v1/usage/summary"
			command := tc.command
			if command == nil {
				command = []string{"billing", "usage", "summary"}
			} else {
				path = "/v1/invocations/inv-correlation"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != path || r.Header.Get("Authorization") != "Bearer fp_correlation-private" {
					t.Error("error correlation changed request target or credentials")
				}
				for _, value := range tc.ids {
					w.Header().Add("X-Faas-Request-Id", value)
				}
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			tg := &target{api: server.URL, cli: configured.cli, home: t.TempDir(), appsDomain: configured.appsDomain}
			for _, mode := range []string{"--json", "--json=false"} {
				result := tg.exec(t, "fp_correlation-private", tg.home, time.Minute, append([]string{mode}, command...)...)
				if result.exit != 3 || len(result.stdout) != 0 {
					t.Fatalf("error output changed: exit=%d stderr=%s", result.exit, result.stderr)
				}
				if mode == "--json" {
					var problem struct {
						ID       string `json:"request_id"`
						Category string `json:"category"`
						Exit     int    `json:"exit_code"`
						Recovery struct {
							ID string `json:"invocation_id"`
						} `json:"recovery"`
					}
					if err := json.Unmarshal(result.stderr, &problem); err != nil || problem.ID != tc.want || problem.Exit != 3 || problem.Category != "temporary_failure" {
						t.Fatalf("invalid correlated error: %s", result.stderr)
					}
					if tc.want == "" && bytes.Contains(result.stderr, []byte(`"request_id"`)) {
						t.Fatal("missing request ID should be omitted")
					}
					if tc.command != nil && problem.Recovery.ID != "inv-correlation" {
						t.Fatalf("correlation replaced invocation recovery: %s", result.stderr)
					}
				} else if tc.want != "" {
					if bytes.Count(result.stderr, []byte("Request ID: "+tc.want)) != 1 {
						t.Fatalf("text correlation missing: %s", result.stderr)
					}
				} else if bytes.Contains(result.stderr, []byte("Request ID:")) {
					t.Fatal("untrusted request ID rendered")
				}
				for _, secret := range []string{"fp_correlation-private", "body-private", "header-private", "proxy-private"} {
					if strings.Contains(string(result.stderr), secret) {
						t.Fatal("untrusted request or response content exposed")
					}
				}
			}
		})
	}
}
