package trafficguard

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPrepareMCPAccessPolicy(t *testing.T) {
	for _, name := range []string{"legacy", "valid", "bad fingerprint", "credentials", "http", "query", "fragment", "serving ID", "positive revision", "zero revision", "negative revision"} {
		t.Run(name, func(t *testing.T) {
			request := api.UpdateDeploymentTrafficRequest{TrafficPercent: 100}
			if name != "legacy" {
				request.ExpectedMCPAccessPolicy = &api.MCPAccessPolicyExpectation{Resource: "https://reports.example/mcp", Fingerprint: strings.Repeat("a", 64)}
			}
			switch name {
			case "positive revision", "zero revision", "negative revision":
				revision := int64(42)
				if name == "zero revision" {
					revision = 0
				}
				if name == "negative revision" {
					revision = -1
				}
				request.ExpectedMCPAccessPolicy.AccountRevision = &revision
			case "bad fingerprint":
				request.ExpectedMCPAccessPolicy.Fingerprint = "private body sentinel"
			case "credentials":
				request.ExpectedMCPAccessPolicy.Resource = "https://private:secret@reports.example/mcp"
			case "http":
				request.ExpectedMCPAccessPolicy.Resource = "http://reports.example/mcp"
			case "query":
				request.ExpectedMCPAccessPolicy.Resource += "?secret=value"
			case "fragment":
				request.ExpectedMCPAccessPolicy.Resource += "#secret"
			case "serving ID":
				id := "not-an-id"
				request.ExpectedServingDeploymentID = &id
			}
			_, problem := Prepare(context.Background(), "account", "app", &request)
			valid := name == "legacy" || name == "valid" || name == "positive revision"
			if (problem == nil) != valid {
				t.Fatalf("problem=%v", problem)
			}
			if problem != nil && strings.Contains(problem.Detail, "private") {
				t.Fatal("validation leaked supplied secret")
			}
		})
	}
}
