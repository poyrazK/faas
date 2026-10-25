package main_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `invocations list` is account-wide but printed no app, so a failed
// "POST /healthz" could not be traced to one (Hunt 12 H12-33). The app slug
// and source follow the existing five columns.
func TestInvocationsListNamesTheApp(t *testing.T) {
	tests := []struct {
		name, appID, want string
	}{
		{"live app", "a-1", "i-1\t2026-10-22T15:01:06Z\tfailed\tPOST\t/healthz\tshop\tcron\n"},
		{"deleted app", "a-9", "i-1\t2026-10-22T15:01:06Z\tfailed\tPOST\t/healthz\t(deleted) a-9\tcron\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/apps":
					_, _ = w.Write([]byte(`[{"id":"a-1","slug":"shop"}]`))
				case "/v1/invocations":
					_, _ = w.Write([]byte(`{"invocations":[{"id":"i-1","created_at":"2026-10-22T15:01:06Z","state":"failed","method":"POST","path":"/healthz","app_id":"` + tt.appID + `","source":"cron"}]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_x")
			out, restore := main.CaptureStdout(t)
			code := main.CmdInvocationsList(nil)
			restore()
			if code != 0 || !strings.Contains(out.String(), tt.want) {
				t.Fatalf("exit %d, want row %q in:\n%s", code, tt.want, out.String())
			}
		})
	}
}
