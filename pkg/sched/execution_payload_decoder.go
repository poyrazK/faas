package sched

import (
	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/executionpayload"
)

// NewAgeExecutionPayloadDecoder wires the scheduler-owned authenticated
// payload boundary. Host identities are copied by executionpayload.NewDecoder
// and are tried current-first/previous-second during key rotation.
func NewAgeExecutionPayloadDecoder(identities []*age.X25519Identity) ExecutionPayloadDecoder {
	if len(identities) == 0 {
		return nil
	}
	return executionpayload.NewDecoder(identities)
}
