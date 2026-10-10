package state

import (
	"context"
	"fmt"
)

type ManagedRealtimeSequenceConflict struct{ Expected, Current int64 }

func (e *ManagedRealtimeSequenceConflict) Error() string {
	return fmt.Sprintf("expected channel sequence %d; current sequence is %d", e.Expected, e.Current)
}

type ManagedRealtimeConditionalStore interface {
	AppendManagedRealtimeChannelConditional(context.Context, string, string, []byte, bool, string, map[string]string, *int64) (ManagedRealtimeChannelMessage, error)
	AppendManagedRealtimeBatchConditional(context.Context, string, string, string, []ManagedRealtimeBatchItem, *int64) ([]ManagedRealtimeChannelMessage, error)
}

func checkExpectedRealtimeSequence(expected *int64, current int64) error {
	if expected == nil {
		return nil
	}
	if *expected < 0 {
		return ErrManagedRealtimeHistoryInvalid
	}
	if *expected != current {
		return &ManagedRealtimeSequenceConflict{Expected: *expected, Current: current}
	}
	return nil
}
