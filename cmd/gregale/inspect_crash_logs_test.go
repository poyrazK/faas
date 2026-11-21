package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// prod hunt 2026-11-21: inspect sent a crashed release's owner to
// `gregale logs APP --deployment ID`, the build and deploy log, which never
// holds what the app printed. A crash points to the runtime logs since the
// release was created; other failures keep the deployment log.
func TestInspectCrashPointsToRuntimeLogs(t *testing.T) {
	for _, tc := range []struct {
		name, code, want, notWant string
	}{
		{"startup crash", api.CodeAppStartupCrash, "logs bh15-py --since 2026-11-21T07:52:49Z", "--deployment"},
		{"build failure", "build_failed", "logs bh15-py --deployment d1", "--since"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			main.RenderInspectFailureDetails(&out, "bh15-py", api.DeploymentResponse{
				ID: "d1", Status: "failed", ErrorCode: tc.code, Error: "it failed", CreatedAt: "2026-11-21T07:52:49.120Z",
			})
			got := out.String()
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
				t.Fatalf("failure details:\n%s\nwant %q and no %q", got, tc.want, tc.notWant)
			}
		})
	}
}
