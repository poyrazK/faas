// Package alertwire defines the small transport boundary for pipeline health
// notifications. Catalog state must not import the outbound daemon stack.
package alertwire

import "time"

type Attempt struct {
	ID, URL, Secret, PipelineID, IncidentID, Kind, Code string
	OccurredAt, Now                                     time.Time
	Number                                              int
}

type Result struct {
	StatusCode                   int
	RetryAfter                   string
	Accepted, Redirect, Terminal bool
}
