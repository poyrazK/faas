package main

import (
	"encoding/json"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// Matches apid's defaultJSONBodyMaxBytes for the event request envelope.
const workflowEventRequestMaxBytes int64 = 1 << 20

func workflowEventPayload(inline, path string, inlineSet, fileSet bool, eventName string) (json.RawMessage, error) {
	payload, err := workflowJSONInput(inline, path, inlineSet, fileSet, "payload", workflowEventRequestMaxBytes)
	if err != nil {
		return nil, err
	}
	request, err := json.Marshal(api.InjectWorkflowEventRequest{EventName: eventName, Payload: payload})
	if err != nil {
		return nil, err
	}
	if int64(len(request)) > workflowEventRequestMaxBytes {
		return nil, fmt.Errorf("workflow event request, including the event name and JSON envelope, exceeds the %d-byte limit", workflowEventRequestMaxBytes)
	}
	return payload, nil
}
