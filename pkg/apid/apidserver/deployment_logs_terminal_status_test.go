package apidserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestDeploymentLogsSSE_CancelledDeploymentEndsStream covers H10-48: the
// live tail ended only on live or failed, so a cancelled deployment kept the
// stream (and a waiting `gregale deploy`) open until the 10-minute backstop.
// Every terminal status now ends it with a status frame.
func TestDeploymentLogsSSE_CancelledDeploymentEndsStream(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	dep := apidserver.MustSeedDeployment(t, e, "cancel-stream")
	if _, _, err := (*e.Store()).CancelDeploymentTx(t.Context(), dep.ID, "test", state.CancelReasonUser); err != nil {
		t.Fatalf("CancelDeploymentTx: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/deployments/"+dep.ID+"/logs", nil)
	req.Header.Set("Authorization", "Bearer "+*e.Key())
	rec := httptest.NewRecorder()
	(*e.H()).ServeHTTP(rec, req)

	if ctx.Err() != nil {
		t.Fatalf("stream stayed open for a cancelled deployment until the test deadline\n%s", rec.Body)
	}
	if want := "event: status\ndata: {\"status\":\"cancelled\"}"; !strings.Contains(rec.Body.String(), want) {
		t.Fatalf("stream body missing %q\n%s", want, rec.Body)
	}
}
