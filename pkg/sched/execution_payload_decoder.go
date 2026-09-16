package sched

import (
	"context"
	"encoding/json"

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
	return func(ctx context.Context, sealed []byte, kid string) (string, json.RawMessage, error) {
		return executionpayload.Decode(ctx, identities, sealed, kid)
	}
}

// NewAgeExecutionBundlePayloadDecoder is the production decoder for the
// ephemeral source-bundle contract. It is separate from the legacy helper so
// existing one-file scheduler seams remain source-compatible.
func NewAgeExecutionBundlePayloadDecoder(identities []*age.X25519Identity) ExecutionPayloadDecoderV2 {
	if len(identities) == 0 {
		return nil
	}
	return executionpayload.NewDecoder(identities)
}
