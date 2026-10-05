// adr: 460 — platform verification exhaustion is distinct from app failure.
package api

import (
	"net/http"
	"testing"
)

func TestStatusForCode_HostingVerificationUnavailable(t *testing.T) {
	if got := StatusForCode(CodeDeploymentVerificationUnavailable); got != http.StatusServiceUnavailable {
		t.Fatalf("platform verification unavailable maps to %d, want 503", got)
	}
	if CodeDeploymentVerificationUnavailable == CodeDeploymentSmokeFailed {
		t.Fatal("platform recovery exhaustion is indistinguishable from application smoke failure")
	}
}
