package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// Discovery recovers durable work after a lost notification or scheduler
// restart. IDs are advisory: claim and execution must still validate the
// complete approved graph, current app owner and original attempt. No lease,
// frozen input or private cleanup capability is exposed by this read.
//
// A pass uses the last ID as its exclusive cursor, then starts again at the
// beginning. A newly eligible ID behind the cursor is found on the next pass.
// Jobs remain excluded until their separate qualification adapter exists.
type EnvironmentGitOpsQualificationDiscoveryStore interface {
	ListEnvironmentWorkloadQualificationsForDispatch(context.Context, string, string, int) ([]string, error)
}

func qualificationDispatchPageValid(nodeID, afterRequestID string, limit int) bool {
	return qualificationRecoveryUUIDValid(nodeID) && (afterRequestID == "" || qualificationRecoveryUUIDValid(afterRequestID)) &&
		limit > 0 && limit <= api.EnvironmentGitOpsQualificationDispatchBatchMax
}
