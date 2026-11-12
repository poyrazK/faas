package state

import (
	"encoding/json"
	"fmt"
)

// ValidateWorkflowEventPayload rejects the internal timeout marker at every
// external event/callback ingress, matching the simulator's reserved shape.
func ValidateWorkflowEventPayload(payload json.RawMessage) error {
	if err := validateWorkflowJSON(payload, false); err != nil {
		return err
	}
	var value map[string]json.RawMessage
	if json.Unmarshal(payload, &value) == nil && len(value) == 1 && string(value["timeout"]) == "true" {
		return fmt.Errorf("%w: event payload is the reserved timeout marker", ErrWorkflowInvalidInput)
	}
	return nil
}
