package apidserver_test

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

func TestPipelineSetupDiscoveryRefusalsDoNotFreezeIdempotency(t *testing.T) {
	for _, code := range []string{"data_connection_check_required", "data_connection_check_busy"} {
		if apidserver.IdempotencyResponseReplayable(http.StatusConflict, []byte(`{"code":"`+code+`"}`)) {
			t.Fatal("temporary discovery refusal froze a resumable setup request", code)
		}
	}
	if !apidserver.IdempotencyResponseReplayable(http.StatusAccepted, []byte(`{"id":"saved-setup"}`)) {
		t.Fatal("accepted setup lost its request receipt")
	}
}
