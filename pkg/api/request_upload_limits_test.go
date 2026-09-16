// adr: 093
package api

import (
	"testing"
	"time"
)

func TestRequestUploadTimeoutCoversAdvertisedPlanBody(t *testing.T) {
	for _, plan := range []Plan{PlanFree, PlanHobby, PlanPro, PlanScale} {
		plan := plan
		t.Run(string(plan), func(t *testing.T) {
			bodySeconds := (plan.MaxRequestBodyBytes() + RequestUploadMinBytesPerSecond - 1) / RequestUploadMinBytesPerSecond
			minimum := RequestUploadTimeoutBase + time.Duration(bodySeconds)*time.Second
			if got := plan.RequestUploadTimeout(); got < minimum {
				t.Fatalf("RequestUploadTimeout() = %s, need at least %s for %d bytes", got, minimum, plan.MaxRequestBodyBytes())
			}
			if got := plan.RequestUploadTimeout(); got > RequestUploadTimeoutMax {
				t.Fatalf("RequestUploadTimeout() = %s, exceeds max %s", got, RequestUploadTimeoutMax)
			}
		})
	}
	if got := PlanScale.RequestUploadTimeout(); got != RequestUploadTimeoutMax {
		t.Fatalf("Scale timeout = %s, want max %s", got, RequestUploadTimeoutMax)
	}
}
