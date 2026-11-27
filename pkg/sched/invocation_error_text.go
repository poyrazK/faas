package sched

import "strings"

// invocationErrorText is an invocation failure as the customer reads it in
// last_error (`invoke`, `invocation get`, cron runs). ErrPermanentInvoke's
// text is an internal classification: a thrown handler read "invoke: sched:
// permanent invoke error: application returned HTTP 500: …" (prod hunt r27).
// The sentinel stays in the error chain, so failOutcome and retry decisions,
// which use errors.Is, are unchanged.
func invocationErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.Replace(err.Error(), ErrPermanentInvoke.Error()+": ", "", 1)
}
