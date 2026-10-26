package main_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ADR-20261026145832: `app health --explain` and `--errors` show what
// `inspect` and `inspect --errors` show, so an app's health has one home.
func TestAppHealthExplainMatchesInspect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/demo":
			_, _ = fmt.Fprint(w, `{"slug":"demo","id":"11111111-1111-1111-1111-111111111111","status":"running","plan":"pro"}`)
		case "/v1/apps/demo/deployments":
			_, _ = fmt.Fprint(w, `{"deployments":[]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprint(w, `{"title":"not found","status":404,"code":"not_found"}`)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")

	for _, pair := range [][2][]string{
		{{"inspect", "demo"}, {"app", "demo", "health", "--explain"}},
		{{"inspect", "demo", "--errors"}, {"app", "demo", "health", "--errors"}},
	} {
		wantCode, wantOut, wantErr := runCLI(t, pair[0]...)
		gotCode, gotOut, gotErr := runCLI(t, pair[1]...)
		if gotCode != wantCode || gotOut != wantOut || gotErr != wantErr {
			t.Errorf("%v = %d\n%s%s\nwant %v = %d\n%s%s", pair[1], gotCode, gotOut, gotErr, pair[0], wantCode, wantOut, wantErr)
		}
		if wantOut == "" && wantErr == "" {
			t.Errorf("%v printed nothing; the comparison proves nothing", pair[0])
		}
	}
}

func TestAppHealthExplainStandsAlone(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "test-token")
	for _, args := range [][]string{
		{"app", "demo", "health", "--explain", "--wait"},
		{"app", "demo", "health", "--explain", "--errors"},
		{"app", "demo", "health", "--errors", "--history"},
	} {
		code, _, stderr := runCLI(t, args...)
		if code == 0 || !strings.Contains(stderr, "stand alone") {
			t.Errorf("%v = %d %q, want a usage error", args, code, stderr)
		}
	}
}
