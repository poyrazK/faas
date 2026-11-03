package api

// EventPublishVerdictBootArg is the kernel command-line flag vmmd adds to an
// app VM's cold boot (ADR-20261031102000). It tells guest-init that this vmmd
// answers an in-guest event publish with its verdict, so the in-guest publish
// endpoint returns vmmd's answer instead of an unconditional 202. A restored
// VM learns the same from its resume request, because its snapshot may have
// booted under a vmmd of either kind.
const EventPublishVerdictBootArg = "faas.eventpublish=verdict"

// EventPublishVerdictMaxBytes bounds vmmd's encoded verdict on the wire.
const EventPublishVerdictMaxBytes = 512

// EventPublishVerdict is vmmd's answer to an in-guest event publish: the
// HTTP status the in-guest endpoint returns, a stable error code when the
// event was refused, and how long to wait before publishing the same event
// again. The codes match POST /v1/events:publish where one exists.
type EventPublishVerdict struct {
	Status            int    `json:"status"`
	Code              string `json:"code,omitempty"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}
