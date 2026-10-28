package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// `gregale deploy-tokens` reaches every deploy-token route (#5709: the
// capability was GA with no CLI command).
func TestDeployTokensReachEachRoute(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantMethod string
		wantPath   string
		wantBody   string
		reply      any
		wantOut    string
	}{
		{
			name:       "list",
			args:       []string{"deploy-tokens", "list", "--app", "web"},
			wantMethod: http.MethodGet,
			wantPath:   "/v1/apps/web/deploy-tokens",
			reply:      api.ListDeployTokensResponse{Tokens: []api.DeployTokenResponse{{ID: "dt-1", Prefix: "fdt_ab", Status: "active", Label: "ci", ExpiresAt: "2027-01-01T00:00:00Z"}}},
			wantOut:    "fdt_ab",
		},
		{
			name:       "add",
			args:       []string{"deploy-tokens", "add", "--app", "web", "--label", "github-actions", "--expires-at", "2027-01-31T00:00:00Z"},
			wantMethod: http.MethodPost,
			wantPath:   "/v1/apps/web/deploy-tokens",
			wantBody:   `{"label":"github-actions","expires_at":"2027-01-31T00:00:00Z"}`,
			reply:      api.DeployTokenResponse{ID: "dt-2", Plaintext: "fdt_secret"},
			wantOut:    "fdt_secret",
		},
		{
			name:       "rotate",
			args:       []string{"deploy-tokens", "rotate", "dt-1", "--app", "web"},
			wantMethod: http.MethodPost,
			wantPath:   "/v1/apps/web/deploy-tokens/dt-1/rotate",
			wantBody:   `{}`,
			reply:      api.RotateDeployTokenResponse{Token: api.DeployTokenResponse{ID: "dt-3"}, TokenPlaintext: "fdt_new", OldTokenID: "dt-1"},
			wantOut:    "fdt_new",
		},
		{
			name:       "rm",
			args:       []string{"deploy-tokens", "rm", "dt-1", "--app", "web"},
			wantMethod: http.MethodDelete,
			wantPath:   "/v1/apps/web/deploy-tokens/dt-1",
			wantOut:    "Revoked deploy token dt-1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			var gotMethod, gotPath, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				var body json.RawMessage
				if r.Body != nil && json.NewDecoder(r.Body).Decode(&body) == nil {
					gotBody = string(body)
				}
				if tc.reply == nil {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_ = json.NewEncoder(w).Encode(tc.reply)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run(tc.args)
			restore()
			if code != 0 {
				t.Fatalf("exit %d, output:\n%s", code, out.String())
			}
			if gotMethod != tc.wantMethod || gotPath != tc.wantPath {
				t.Fatalf("request %s %s, want %s %s", gotMethod, gotPath, tc.wantMethod, tc.wantPath)
			}
			if tc.wantBody != "" && gotBody != tc.wantBody {
				t.Fatalf("body %s, want %s", gotBody, tc.wantBody)
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("output lacks %q:\n%s", tc.wantOut, out.String())
			}
		})
	}
}

func TestDeployTokensRejectBadArgumentsWithoutACall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no request expected for invalid arguments")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test_x")
	for _, args := range [][]string{
		{"deploy-tokens"},
		{"deploy-tokens", "frobnicate"},
		{"deploy-tokens", "list"},
		{"deploy-tokens", "rm", "--app", "web"},
		{"deploy-tokens", "rotate", "--app", "web"},
		{"deploy-tokens", "add", "--app", "web", "--expires-at", "next week"},
	} {
		if code := main.Run(args); code == 0 {
			t.Errorf("%v exit 0, want an argument error", args)
		}
	}
}
