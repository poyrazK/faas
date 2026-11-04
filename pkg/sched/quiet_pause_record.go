package sched

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// QuietPauseRecord is vmmd's quiet pause record for one instance
// incarnation, the input of the pause ledger that meterd subtracts from
// billed seconds (ADR-20261023171905).
type QuietPauseRecord struct {
	// Since is when the current quiet pause began; zero while running.
	Since time.Time
	// LastStarted and LastEnded bound the incarnation's most recent
	// completed pause; both are zero before its first resume.
	LastStarted, LastEnded time.Time
	// PausedTotal sums the incarnation's completed pauses; nil when vmmd
	// did not report it.
	PausedTotal *time.Duration
}

// QuietPauseRecordFromProto decodes the quiet pause fields that vmmd's
// InstanceStats and the capacity stream's InstanceTelemetry share. A last
// pause missing either bound is dropped.
func QuietPauseRecordFromProto(since, lastStarted, lastEnded *timestamppb.Timestamp, total *wrapperspb.Int64Value) QuietPauseRecord {
	var r QuietPauseRecord
	if since != nil {
		r.Since = since.AsTime()
	}
	if lastStarted != nil && lastEnded != nil {
		r.LastStarted, r.LastEnded = lastStarted.AsTime(), lastEnded.AsTime()
	}
	if total != nil {
		d := time.Duration(total.GetValue())
		r.PausedTotal = &d
	}
	return r
}
