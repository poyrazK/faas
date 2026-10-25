package api

import "net/http"

// CodeRehearsalNotEnabled refuses a rehearsal capability this installation has
// not qualified and enabled (the FAAS_REHEARSAL_* gates). Unlike
// capacity_unavailable it does not clear on retry, so clients stop instead of
// retrying.
const CodeRehearsalNotEnabled = "rehearsal_not_enabled"

// ErrRehearsalNotEnabled names the rehearsal capability that is switched off.
func ErrRehearsalNotEnabled(capability string) *Problem {
	return NewProblem(http.StatusNotImplemented, CodeRehearsalNotEnabled,
		"Rehearsal capability not enabled",
		capability+" is not enabled on this Gregale installation. Retrying will not help; remove it from the scenario.")
}
