package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSensitiveMutationJSONContract(t *testing.T) {
	const alertID = "0123456789abcdef0123456789abcdef"
	cases := []struct {
		name       string
		args       []string
		stdin      string
		jsonEnv    bool
		method     string
		path       string
		query      string
		secretBody string
		restart    bool
	}{
		{
			name:   "secrets set from argv with flag",
			args:   []string{"--json", "secrets", "set", "--app", "demo", "--scope", "staging", "TOKEN=argv-secret"},
			method: http.MethodPut, path: "/v1/apps/demo/secrets/TOKEN", query: "scope=staging", secretBody: "argv-secret",
		},
		{
			name:  "secrets set from stdin with environment",
			args:  []string{"secrets", "set", "--app", "demo", "--scope", "staging", "--from-stdin"},
			stdin: "TOKEN=stdin-secret\n", jsonEnv: true,
			method: http.MethodPut, path: "/v1/apps/demo/secrets/TOKEN", query: "scope=staging", secretBody: "stdin-secret",
		},
		{
			name:   "secrets set with restart",
			args:   []string{"--json", "secrets", "set", "--app", "demo", "--scope", "staging", "TOKEN=argv-secret", "--restart"},
			method: http.MethodPut, path: "/v1/apps/demo/secrets/TOKEN", query: "scope=staging", secretBody: "argv-secret", restart: true,
		},
		{
			name:   "secrets rotate from argv with flag",
			args:   []string{"--json", "secrets", "rotate", "--app", "demo", "--scope", "staging", "TOKEN=argv-secret"},
			method: http.MethodPost, path: "/v1/apps/demo/secrets/TOKEN/rotate", query: "scope=staging", secretBody: "argv-secret",
		},
		{
			name:  "secrets rotate from stdin with environment",
			args:  []string{"secrets", "rotate", "--app", "demo", "--scope", "staging", "--from-stdin"},
			stdin: "TOKEN=stdin-secret\n", jsonEnv: true,
			method: http.MethodPost, path: "/v1/apps/demo/secrets/TOKEN/rotate", query: "scope=staging", secretBody: "stdin-secret",
		},
		{
			name:   "secrets rotate with restart",
			args:   []string{"--json", "secrets", "rotate", "--app", "demo", "--scope", "staging", "TOKEN=argv-secret", "--restart"},
			method: http.MethodPost, path: "/v1/apps/demo/secrets/TOKEN/rotate", query: "scope=staging", secretBody: "argv-secret", restart: true,
		},
		{
			name:   "secrets unset",
			args:   []string{"--json", "secrets", "unset", "--app", "demo", "--scope", "staging", "TOKEN"},
			method: http.MethodDelete, path: "/v1/apps/demo/secrets/TOKEN", query: "scope=staging",
		},
		{
			name:   "alert remove",
			args:   []string{"--json", "alerts", "rm", "--app", "demo", alertID},
			method: http.MethodDelete, path: "/v1/apps/demo/alerts/" + alertID,
		},
		{
			name:   "api key add",
			args:   []string{"--json", "keys", "add", "ci"},
			method: http.MethodPost, path: "/v1/keys",
		},
		{
			name: "api key remove with environment",
			args: []string{"keys", "rm", "key-old"}, jsonEnv: true,
			method: http.MethodDelete, path: "/v1/keys/key-old",
		},
		{
			name: "api key rotate with environment",
			args: []string{"keys", "rotate", "key-old"}, jsonEnv: true,
			method: http.MethodPost, path: "/v1/keys/key-old/rotate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetJSONOut(t)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "test-token")
			if tc.jsonEnv {
				t.Setenv("FAAS_JSON", "1")
			} else {
				t.Setenv("FAAS_JSON", "")
			}

			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if tc.restart && r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/restart" && r.URL.RawQuery == "fresh=true" {
					writeJSONTest(w, api.AppRestartResponse{WakeID: "wake-json-1"})
					return
				}
				if r.Method != tc.method || r.URL.Path != tc.path || r.URL.RawQuery != tc.query {
					t.Errorf("request = %s %s?%s, want %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery, tc.method, tc.path, tc.query)
					http.NotFound(w, r)
					return
				}
				if tc.secretBody != "" {
					var body api.PutAppSecretRequest
					if r.Method == http.MethodPost {
						var rotate api.RotateAppSecretRequest
						if err := json.NewDecoder(r.Body).Decode(&rotate); err != nil {
							t.Errorf("decode rotation body: %v", err)
						} else if rotate.Value != tc.secretBody {
							t.Errorf("rotation value = %q, want supplied stdin/argv value", rotate.Value)
						}
					} else if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Errorf("decode set body: %v", err)
					} else if body.Value != tc.secretBody {
						t.Errorf("set value = %q, want supplied stdin/argv value", body.Value)
					}
				}

				switch tc.path {
				case "/v1/apps/demo/secrets/TOKEN/rotate":
					writeJSONTest(w, api.RotateAppSecretResponse{Key: "TOKEN", RotatedAt: "2026-09-28T12:00:00Z", Kid: "age-test-kid"})
				case "/v1/keys":
					writeJSONTest(w, api.APIKeyResponse{ID: "key-new", OrgID: "org-1", Prefix: "fp_live_test", Scopes: []string{"admin"}, CreatedAt: "2026-09-28T12:00:00Z", Plaintext: "fp_live_one_time"})
				case "/v1/keys/key-old/rotate":
					writeJSONTest(w, api.RotateKeyResponse{Key: api.APIKeyResponse{ID: "key-new", Prefix: "fp_live_new", Scopes: []string{"admin"}}, KeyPlaintext: "fp_live_rotated_once", OldKeyID: "key-old", OldKeyExpiresAt: "2026-10-05T12:00:00Z"})
				default:
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)

			var stdout bytes.Buffer
			oldOut, oldIn := osStdout, osStdin
			osStdout, osStdin = &stdout, strings.NewReader(tc.stdin)
			t.Cleanup(func() { osStdout, osStdin = oldOut, oldIn })

			if code := run(tc.args); code != 0 {
				t.Fatalf("run(%v) = %d; stdout=%q", tc.args, code, stdout.String())
			}
			wantCalls := 1
			if tc.restart {
				wantCalls++
			}
			if calls != wantCalls {
				t.Fatalf("made %d API requests, want %d", calls, wantCalls)
			}
			var receipt map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &receipt); err != nil {
				t.Fatalf("stdout is not one JSON receipt: %v\n%s", err, stdout.String())
			}
			if len(receipt) == 0 {
				t.Fatalf("empty JSON receipt: %s", stdout.String())
			}
			for _, secret := range []string{"argv-secret", "stdin-secret"} {
				if strings.Contains(stdout.String(), secret) {
					t.Fatalf("secret plaintext leaked to stdout: %q", stdout.String())
				}
			}

			switch tc.name {
			case "secrets set from argv with flag", "secrets set from stdin with environment":
				if receipt["status"] != "updated" || receipt["scope"] != "staging" {
					t.Fatalf("set receipt = %#v", receipt)
				}
				keys, ok := receipt["keys"].([]any)
				if !ok || len(keys) != 1 || keys[0] != "TOKEN" {
					t.Fatalf("set receipt keys = %#v", receipt["keys"])
				}
				warnings, ok := receipt["warnings"].([]any)
				if !ok || len(warnings) == 0 {
					t.Fatalf("set receipt omitted its deferred-application warning: %#v", receipt)
				}
			case "secrets set with restart":
				if receipt["status"] != "updated" || receipt["restart_requested"] != true || receipt["wake_id"] != "wake-json-1" {
					t.Fatalf("set restart receipt = %#v", receipt)
				}
			case "secrets rotate from argv with flag", "secrets rotate from stdin with environment":
				if receipt["status"] != "rotated" || receipt["key"] != "TOKEN" || receipt["rotated_at"] != "2026-09-28T12:00:00Z" || receipt["kid"] != "age-test-kid" {
					t.Fatalf("rotate receipt = %#v", receipt)
				}
				warnings, ok := receipt["warnings"].([]any)
				if !ok || len(warnings) == 0 {
					t.Fatalf("rotate receipt omitted its deferred-application warning: %#v", receipt)
				}
			case "secrets rotate with restart":
				if receipt["status"] != "rotated" || receipt["restart_requested"] != true || receipt["wake_id"] != "wake-json-1" {
					t.Fatalf("rotate restart receipt = %#v", receipt)
				}
			case "secrets unset":
				if receipt["status"] != "deleted" || receipt["key"] != "TOKEN" || receipt["deleted"] != true {
					t.Fatalf("unset receipt = %#v", receipt)
				}
			case "alert remove":
				if receipt["app"] != "demo" || receipt["id"] != alertID || receipt["deleted"] != true {
					t.Fatalf("alert remove receipt = %#v", receipt)
				}
			case "api key add":
				if receipt["id"] != "key-new" || receipt["plaintext"] != "fp_live_one_time" {
					t.Fatalf("key add receipt = %#v", receipt)
				}
			case "api key remove with environment":
				if receipt["id"] != "key-old" || receipt["revoked"] != true {
					t.Fatalf("key remove receipt = %#v", receipt)
				}
			case "api key rotate with environment":
				if receipt["key_plaintext"] != "fp_live_rotated_once" || receipt["old_key_id"] != "key-old" {
					t.Fatalf("key rotate receipt = %#v", receipt)
				}
			default:
				t.Fatalf("unhandled test case %q: %#v", tc.name, receipt)
			}
		})
	}
}
