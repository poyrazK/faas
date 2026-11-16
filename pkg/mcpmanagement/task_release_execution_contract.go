package mcpmanagement

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

// Task execution is bound to a native intent and operation, independently of
// generic deployment plans. Validate both identities without rewriting evidence.
func bindTaskExecutionReceipt(args any, data json.RawMessage) (json.RawMessage, error) {
	var request applyTaskExecutionArgs
	raw, err := json.Marshal(args)
	if err != nil || json.Unmarshal(raw, &request) != nil || !toolpublication.ValidID(request.IntentID) || !toolpublication.ValidID(request.OperationID) {
		return nil, invalid("invalid_response", "Task apply request has no exact intent and operation identity")
	}
	var record api.TaskToolReleaseIntentRecord
	if json.Unmarshal(data, &record) != nil {
		return nil, invalid("invalid_response", "Task apply returned no native receipt")
	}
	if _, err = validateTaskIntentReceipt(record, request.IntentID); err != nil {
		return nil, err
	}
	if record.Execution == nil || record.Execution.OperationID != request.OperationID {
		return nil, invalid("invalid_response", "Task apply operation identity mismatch")
	}
	return data, nil
}
