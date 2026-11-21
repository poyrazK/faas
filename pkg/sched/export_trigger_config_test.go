package sched

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OpenTriggerConfigForTest exposes openTriggerConfig (ADR-20261110210000
// slice 3g) to sched_test.
func (l *Loop) OpenTriggerConfigForTest(t sqlc.Trigger) (json.RawMessage, error) {
	return l.openTriggerConfig(context.Background(), t)
}

// ErrTriggerWithoutApp is errTriggerWithoutApp for sched_test.
var ErrTriggerWithoutApp = errTriggerWithoutApp
