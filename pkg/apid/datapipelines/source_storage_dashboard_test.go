package datapipelines

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestSourceStorageVerificationRetryRequiresCurrentStoppedProof(t *testing.T) {
	for _, reason := range []string{"source_storage_verification", "replication_continuity_verification"} {
		for _, state := range []string{"cancelled", "limited", "running", "passed"} {
			p := datapipeline.Pipeline{DataPipeline: api.DataPipeline{DesiredState: "running", State: "blocked", Reason: reason, ObservationFresh: true}, VerificationProgress: &datapipeline.VerificationProgress{State: state}}
			if view := verificationView(p); view.CanRetry != (state == "cancelled" || state == "limited") {
				t.Fatal("wrong retry eligibility", state, view.CanRetry)
			}
			p.ObservationFresh = false
			if verificationView(p).CanRetry {
				t.Fatal("stale proof offered retry", state)
			}
		}
	}
}
