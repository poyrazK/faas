// adr: 1139
package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type projectSecretCall struct{ method, path, value string }

func projectSecretsServer(t *testing.T) (*[]projectSecretCall, func()) {
	t.Helper()
	var mu sync.Mutex
	calls := []projectSecretCall{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body api.SetProjectEnvironmentSharedSecretRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, projectSecretCall{r.Method, r.URL.Path, body.Value})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentSharedSecretsResponse{ProjectSlug: "shop", Environment: "production", Limit: 50,
				Secrets: []api.ProjectEnvironmentSharedSecret{{Key: "DATABASE_PASSWORD", Version: 3, UpdatedAt: "2026-10-21T10:00:00Z"}}})
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			key := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/projects/shop/environments/production/secrets/"), "/rotate")
			_ = json.NewEncoder(w).Encode(api.ProjectEnvironmentSharedSecretResponse{ProjectSlug: "shop", Environment: "production",
				Secret: api.ProjectEnvironmentSharedSecret{Key: key, Version: 1}})
		}
	}))
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	return &calls, srv.Close
}

func runProjectSecrets(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	out := &bytes.Buffer{}
	oldOut, oldIn := *main.OsStdout, *main.OsStdin
	*main.OsStdout, *main.OsStdin = out, strings.NewReader(stdin)
	defer func() { *main.OsStdout, *main.OsStdin = oldOut, oldIn }()
	code := main.CmdProjects(append([]string{"environments", "secrets"}, args...))
	return code, out.String()
}

func TestProjectEnvironmentSecretsCLI(t *testing.T) {
	calls, stop := projectSecretsServer(t)
	defer stop()
	base := "/v1/projects/shop/environments/production/secrets"

	code, out := runProjectSecrets(t, "", "shop", "production")
	if code != 0 || !strings.Contains(out, "Shared secrets shop/production (1 of 50)") || !strings.Contains(out, "DATABASE_PASSWORD  v3") {
		t.Fatalf("list = %d %q", code, out)
	}
	if code, out = runProjectSecrets(t, "", "set", "shop", "production", "DATABASE_PASSWORD=pa=ss", "API_KEY=k"); code != 0 {
		t.Fatalf("set = %d %q", code, out)
	}
	if strings.Contains(out, "pa=ss") {
		t.Fatalf("set echoed a value: %q", out)
	}
	if code, out = runProjectSecrets(t, "# comment\nSIGNING_KEY=from-stdin\n", "rotate", "shop", "production", "--from-stdin"); code != 0 {
		t.Fatalf("rotate = %d %q", code, out)
	}
	if code, out = runProjectSecrets(t, "", "unset", "shop", "production", "API_KEY"); code != 0 || !strings.Contains(out, "Removed API_KEY") {
		t.Fatalf("unset = %d %q", code, out)
	}
	want := []projectSecretCall{
		{"GET", base, ""},
		{"PUT", base + "/DATABASE_PASSWORD", "pa=ss"},
		{"PUT", base + "/API_KEY", "k"},
		{"POST", base + "/SIGNING_KEY/rotate", "from-stdin"},
		{"DELETE", base + "/API_KEY", ""},
	}
	if len(*calls) != len(want) {
		t.Fatalf("calls = %+v", *calls)
	}
	for i := range want {
		if (*calls)[i] != want[i] {
			t.Fatalf("call %d = %+v, want %+v", i, (*calls)[i], want[i])
		}
	}

	// Exactly one value source, and nothing is sent when the input is wrong.
	before := len(*calls)
	for _, args := range [][]string{
		{"set", "shop", "production"},
		{"set", "shop", "production", "A=1", "--from-stdin"},
		{"set", "shop", "production", "NOT_A_PAIR"},
		{"unset", "shop", "production"},
		{"bad project!", "production"},
	} {
		if code, _ := runProjectSecrets(t, "A=1\n", args...); code == 0 {
			t.Fatalf("%v succeeded", args)
		}
	}
	if len(*calls) != before {
		t.Fatalf("invalid input reached the API: %+v", (*calls)[before:])
	}
}
