package state

import "fmt"

// checkRolloutStamp reports whether SafedeployStampRollout may move a rollout
// from current to next. The orchestrator stamps only rollouts still in
// flight: pending ones start, and pending or rolling_out ones complete or
// abort.
//
// Hunt 20 H20-2: the stamp locked the row but wrote the state blindly, so a
// rollout APID aborted after the orchestrator listed it was rewritten as
// complete, losing its abort, or put back to rolling_out, where the canary
// worker raised its traffic again. A row that changed since it was listed
// now keeps its state.
func checkRolloutStamp(current, next string) error {
	current, next = NormalizeRolloutState(current), NormalizeRolloutState(next)
	allowed := false
	switch next {
	case "rolling_out":
		allowed = current == "pending"
	case "complete", "aborted":
		allowed = current == "pending" || current == "rolling_out"
	}
	if !allowed {
		return fmt.Errorf("%w: rollout is %s, cannot stamp %s", ErrRolloutStateInvalid, current, next)
	}
	return nil
}
