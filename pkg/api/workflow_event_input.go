package api

import "net/http"

// ErrWorkflowEventEnvelopeTooLarge reports the limit on the complete event
// envelope, including server-stamped metadata, used as workflow input.
func ErrWorkflowEventEnvelopeTooLarge() *Problem {
	p := NewProblem(http.StatusRequestEntityTooLarge, "event_envelope_too_large", "Event envelope too large", "the event and its envelope exceed the workflow input limit")
	limit := WorkflowRunInputMaxBytes
	p.Limit, p.LimitBytes = &limit, &limit
	return p
}
