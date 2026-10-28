package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us 2026-10-22: rolling h12-go back to its baseline said
// "secret H12_FLAG not restored (revision 1 had version 0, now 1); set it
// with `gregale secrets set`". Revision 1 had no H12_FLAG, so matching it
// means removing the secret (Hunt 12 H12-57).
func TestEnvRollbackSecretAdviceMatchesTheDifference(t *testing.T) {
	tests := []struct {
		name      string
		diff      api.ConfigSecretDifference
		want, not string
	}{
		{"added after the revision", api.ConfigSecretDifference{Key: "H12_FLAG", CurrentVersion: 1},
			"remove it with `gregale env unset --app h12-go H12_FLAG`", "secrets set"},
		{"removed since the revision", api.ConfigSecretDifference{Key: "OLD", TargetVersion: 2},
			"had version 2, now removed); set it with `gregale env set`", "unset"},
		{"rotated since the revision", api.ConfigSecretDifference{Key: "TOKEN", TargetVersion: 2, CurrentVersion: 3},
			"had version 2, now 3); set it with `gregale env set`", "unset"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(api.ConfigRollbackResponse{RestoredRevision: 1, SecretsNotRestored: []api.ConfigSecretDifference{tt.diff}})
			}))
			defer srv.Close()
			t.Setenv("FAAS_API", srv.URL)
			t.Setenv("FAAS_TOKEN", "fp_live_x")
			t.Chdir(t.TempDir())
			main.ResetJSONOut(t)

			out, restore := main.CaptureStdout(t)
			code := main.CmdEnv([]string{"rollback", "1", "--app", "h12-go", "--yes"})
			restore()
			got := out.String()
			if code != 0 || !strings.Contains(got, tt.want) || strings.Contains(got, tt.not) {
				t.Fatalf("exit %d; want %q and no %q in:\n%s", code, tt.want, tt.not, got)
			}
		})
	}
}
