package imaged

import "github.com/onebox-faas/faas/pkg/whycopy"

// releaseTaskInfrastructureFailures are the app-task failure codes schedd's
// coordinator records when the platform, not the release command, failed:
// the task's VM environment could not be prepared or prepared too slowly,
// the result channel closed, the guest answered outside the protocol, an
// interactive session could not be routed, or the worker's lease expired
// after the command started (its outcome is then unknown, and whycopy says
// to check it with `gregale release resolve`). The command's own failures
// (command_failed, a command timeout) and refusals tied to the customer's
// account or database (account inactive, database cutover) are not here.
var releaseTaskInfrastructureFailures = map[string]struct{}{
	"restore_failed":                {},
	"restore_timeout":               {},
	"execution_transport_failed":    {},
	"guest_protocol_error":          {},
	"attach_unroutable":             {},
	whycopy.ReleaseTaskLeaseExpired: {},
}

// releaseTaskInfrastructureFailure reports whether a failed release task's
// failure code is a platform fault.
func releaseTaskInfrastructureFailure(code string) bool {
	_, ok := releaseTaskInfrastructureFailures[code]
	return ok
}
