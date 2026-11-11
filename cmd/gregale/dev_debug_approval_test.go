package main_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261111150000: an agent key's debugger tunnel waits for a person's
// approval; the CLI names the review link instead of a bare HTTP status.
func TestDevDebugTunnelNamesApprovalLink(t *testing.T) {
	required := api.NewProblem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required", "needs approval")
	required.AgentApproval = &api.AgentApprovalPendingInfo{ID: "a1", Category: api.AgentApprovalCredentials, ReviewURL: "https://gregale.test/approvals/a1"}
	cases := []struct {
		name    string
		problem *api.Problem
		want    []string
	}{
		{"approval required", required, []string{"https://gregale.test/approvals/a1", api.DevDebugApprovalWindow.String()}},
		{
			"denied",
			api.NewProblem(http.StatusForbidden, api.CodeApprovalDenied, "Approval denied", "a person denied this credentials request"),
			[]string{"HTTP 403", "a person denied this credentials request"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				api.WriteProblem(w, c.problem)
			}))
			defer srv.Close()
			err := main.DialDevDebugTunnel(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/apps/dev-app/debug", "tok")
			if err == nil {
				t.Fatal("dial succeeded; want a refusal")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
