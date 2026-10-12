package main

import (
	"errors"
	"fmt"
	"net"
)

// A failed submission does not always establish whether the mutation happened.
// Keep the existing error envelope and exit code; human guidance follows it.
func printRunSubmissionError(title string, err error, inspectCommand string, keyed bool) int {
	code := printErr(title, err)
	if jsonOutput || !runSubmissionUncertain(err) {
		return code
	}
	_, _ = fmt.Fprintln(osStderr, "\nCould not confirm whether the run was created. Submitting again may create another run.")
	_, _ = fmt.Fprintf(osStderr, "Check recent runs before submitting again (POSIX shells):\n  %s\n", redactErrorText(inspectCommand))
	_, _ = fmt.Fprintln(osStderr, "An empty list does not prove that the submission failed; the original request may still be in progress.")
	if keyed {
		_, _ = fmt.Fprintln(osStderr, "If you retry, use the original --idempotency-key with the same input, app, workflow, profile, and account. Server replay retention limits still apply.")
	}
	return code
}

func runSubmissionUncertain(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		// An explicit rejection retains its existing server remediation.
		return apiErr.Problem.Status >= 500 || apiErr.Problem.Status == 408
	}
	var connectionErr *net.OpError
	if errors.As(err, &connectionErr) && connectionErr.Op == "dial" {
		// No connection was established, so this request was not submitted.
		return false
	}
	// Transport, response-read, and decode errors can occur after acceptance.
	// Callers invoke this only after a validated run submission was attempted.
	return true
}
