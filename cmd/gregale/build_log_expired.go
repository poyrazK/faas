package main

import (
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

// buildLogExpiredFallback is shown for an expired frame without a message.
const buildLogExpiredFallback = "This deployment's build log has expired."

// buildLogExpiredMessage reads an `event: expired` frame of a deployment log
// stream: the server found no rows because build log retention deleted them
// (ADR-20261102182856). ok is false for every other frame.
func buildLogExpiredMessage(e api.Event) (message string, ok bool) {
	if e.Event != api.DeploymentLogExpiredEvent {
		return "", false
	}
	var notice api.DeploymentLogExpiredNotice
	if json.Unmarshal([]byte(e.Data), &notice) != nil || notice.Message == "" {
		return buildLogExpiredFallback, true
	}
	return notice.Message, true
}
