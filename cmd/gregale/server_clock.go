package main

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// serverClockSkewTolerance is how far a timestamp the server stamped may run
// ahead of this machine's clock before the CLI treats it as future-dated.
// The checks below compared server time with the local clock exactly, so a
// laptop ~115 ms behind the server rejected every fresh answer: production-us
// 2026-11-07, `event subscription get` failed 5/5 with "receipt budget is
// stale or future-dated". Every such check shares the one tolerance in
// pkg/api/limits.go (Hunt 32 H32-74).
const serverClockSkewTolerance = api.EventDeliveryObservationClockSkew

// serverTimeAhead reports whether a server timestamp is later than the local
// clock by more than serverClockSkewTolerance.
func serverTimeAhead(t, now time.Time) bool {
	return t.After(now.Add(serverClockSkewTolerance))
}
