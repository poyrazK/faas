package api

import "fmt"

// Pause values an app declares for the pause tiers (ADR-1151): when the
// platform may pause its VM. The platform cannot see in-process timers or
// heartbeat protocols from outside, so the app says what it tolerates.
const (
	// PauseBetweenRequests quiet-pauses the VM between requests and
	// freezes it at the idle timeout. The default for functions.
	PauseBetweenRequests = "between_requests"
	// PauseWhenIdle runs the VM through the idle timeout, then freezes it.
	// The default for request-mode apps.
	PauseWhenIdle = "when_idle"
	// PauseNever never pauses or freezes the VM; it parks at the idle
	// timeout.
	PauseNever = "never"
)

// ValidatePause rejects an unknown pause value. Empty selects the default
// for the app's type.
func ValidatePause(pause string) error {
	switch pause {
	case "", PauseBetweenRequests, PauseWhenIdle, PauseNever:
		return nil
	}
	return fmt.Errorf("pause %q must be one of %s, %s or %s", pause, PauseBetweenRequests, PauseWhenIdle, PauseNever)
}

// EffectivePause resolves a declared pause value: empty means
// between_requests for a function and when_idle for any other app.
func EffectivePause(pause string, function bool) string {
	switch {
	case pause != "":
		return pause
	case function:
		return PauseBetweenRequests
	default:
		return PauseWhenIdle
	}
}
