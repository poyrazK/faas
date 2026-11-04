package main_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `webhooks rm --app <slug> <id> --yes` failed with "unknown flag --yes"
// although every other rm takes it (Hunt 12).
func TestWebhooksRmAcceptsYes(t *testing.T) {
	const id = "f4a332f6-bf21-4d73-bde3-c7696805c074"
	tests := []struct {
		name string
		args []string
	}{
		{"without --yes", []string{"--app", "web", id}},
		{"with --yes after the id", []string{"--app", "web", id, "--yes"}},
		{"with --yes first", []string{"--yes", "--app", "web", id}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			deleted := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete && r.URL.Path == "/v1/apps/web/webhooks/"+id {
					deleted++
					w.WriteHeader(http.StatusNoContent)
					return
				}
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			_, restore := main.CaptureStdout(t)
			defer restore()
			if code := main.CmdWebhooksRm(tt.args); code != 0 || deleted != 1 {
				t.Fatalf("webhooks rm %q: exit=%d deleted=%d", tt.args, code, deleted)
			}
		})
	}
}
