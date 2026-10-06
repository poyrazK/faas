package events

import "github.com/onebox-faas/faas/pkg/eventcontract"

// Envelope is the canonical structured event contract, shared by the router
// and transactional admission without depending on event emission or state.
type Envelope = eventcontract.Envelope

const (
	CloudEventsSpecVersion = eventcontract.CloudEventsSpecVersion
	JSONDataContentType    = eventcontract.JSONDataContentType
	EnvelopeStringMax      = eventcontract.EnvelopeStringMax
)
