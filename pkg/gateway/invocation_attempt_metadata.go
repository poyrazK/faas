package gateway

import (
	"fmt"
	"math"

	"github.com/google/uuid"
)

func validateInvocationAttemptMetadata(req invocationDispatchRequest) error {
	has := req.InvocationAttempt != 0 || req.InvocationReplayGeneration != 0 || req.InvocationReplayedFrom != "" || req.InvocationReplayRoot != ""
	if !has {
		return nil
	} // Legacy scheduler envelopes have no attempt proof.
	if (req.Source != "async_invoke" && req.Source != "replay") || req.InvocationAttempt < 1 || req.InvocationAttempt > math.MaxInt32 || req.InvocationReplayGeneration < 0 {
		return fmt.Errorf("invalid invocation attempt counters")
	}
	current, err := uuid.Parse(req.InvocationID)
	if err != nil || current == uuid.Nil {
		return fmt.Errorf("invalid invocation identity")
	}
	for _, raw := range []string{req.InvocationReplayedFrom, req.InvocationReplayRoot} {
		if raw == "" {
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return fmt.Errorf("invalid invocation replay lineage")
		}
		if id == current && (raw == req.InvocationReplayedFrom || req.InvocationReplayedFrom != "") {
			return fmt.Errorf("cyclic invocation replay lineage")
		}
	}
	return nil
}
