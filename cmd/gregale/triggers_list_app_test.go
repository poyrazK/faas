package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `triggers list --app h12-q` printed the app as a UUID (Hunt 12 H12-47).
func TestTriggersListNamesTheApp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"filtered by --app", []string{"triggers", "list", "--app", "shop"}},
		{"all apps", []string{"triggers", "list"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps/shop":
					_, _ = w.Write([]byte(`{"id":"a-1","slug":"shop"}`))
				case "/v1/apps":
					_, _ = w.Write([]byte(`[{"id":"a-1","slug":"shop"}]`))
				case "/v1/triggers":
					_, _ = w.Write([]byte(`[{"id":"3e0a494c-184a-4699-81e1-39fac73279e6","kind":"queue","slug":"default","enabled":true,"app_id":"a-1"}]`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.Run(tt.args)
			restore()
			if code != 0 || !strings.Contains(out.String(), "enabled    shop\n") {
				t.Fatalf("exit %d, want the app slug:\n%s", code, out.String())
			}
		})
	}
}
