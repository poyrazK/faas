package apidserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// TestEgressDeniedNamesTheURLNotAWebhookField: a log drain to a private
// address was refused as "webhook_url resolves to 10.0.0.1" on
// production-us rc.294 (Hunt 14 H14-66); log drains have no webhook_url.
func TestEgressDeniedNamesTheURLNotAWebhookField(t *testing.T) {
	problem := apidserver.ResolveAndCheckEgress(context.Background(), "https://10.0.0.1/logs")
	if problem == nil || problem.Code != api.CodeImageEgressDenied {
		t.Fatalf("problem = %+v, want %s", problem, api.CodeImageEgressDenied)
	}
	if strings.Contains(problem.Detail, "webhook_url") || !strings.Contains(problem.Detail, "the URL's host 10.0.0.1 resolves to 10.0.0.1") {
		t.Fatalf("detail = %q", problem.Detail)
	}
}
