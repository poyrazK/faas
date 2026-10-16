package routeprobe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

func TestCriticalProbeRequiresAttributed2xx(t *testing.T) {
	for _, tc := range []struct {
		status int
		proof  bool
		want   Outcome
	}{
		{200, true, Success}, {204, true, Success}, {302, true, UnexpectedStatus}, {404, true, UnexpectedStatus}, {401, true, Unauthenticated}, {403, true, Unauthenticated}, {500, true, ServerError}, {200, false, Unattributed},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != "api.apps.test" || r.Header.Get(DeploymentHeader) != "candidate" || r.Header.Get(TokenHeader) != "token" {
					t.Error("challenge target changed")
				}
				if tc.proof {
					w.Header().Set(apihostingreceipt.ServedResponseHeader, apihostingreceipt.CandidateResponseProof("candidate", "token"))
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			client := Client{BaseURL: server.URL, AppsDomain: "apps.test"}
			got, err := client.ProbeCritical(t.Context(), "api", "candidate", "token", "GET", "/health")
			if err != nil || got != tc.want {
				t.Fatalf("outcome=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
}
