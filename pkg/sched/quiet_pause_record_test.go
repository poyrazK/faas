// adr: 20261023171905 — the quiet pause record decodes the same from vmmd
// Stats and from the capacity stream.
package sched_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/onebox-faas/faas/pkg/sched"
)

func TestQuietPauseRecordFromProto(t *testing.T) {
	at := func(sec int64) *timestamppb.Timestamp { return timestamppb.New(time.Unix(sec, 0)) }
	total := 10 * time.Second
	tests := []struct {
		name                   string
		since, started, ended  *timestamppb.Timestamp
		total                  *wrapperspb.Int64Value
		wantSince              time.Time
		wantStarted, wantEnded time.Time
		wantTotal              *time.Duration
	}{
		{name: "absent"},
		{name: "open pause only", since: at(200), wantSince: time.Unix(200, 0)},
		{name: "completed pause", started: at(150), ended: at(160), total: wrapperspb.Int64(int64(total)),
			wantStarted: time.Unix(150, 0), wantEnded: time.Unix(160, 0), wantTotal: &total},
		{name: "a completed pause missing its end is dropped", started: at(150), total: wrapperspb.Int64(int64(total)), wantTotal: &total},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sched.QuietPauseRecordFromProto(tc.since, tc.started, tc.ended, tc.total)
			if !got.Since.Equal(tc.wantSince) || !got.LastStarted.Equal(tc.wantStarted) || !got.LastEnded.Equal(tc.wantEnded) {
				t.Fatalf("record = %+v, want since %v last [%v, %v]", got, tc.wantSince, tc.wantStarted, tc.wantEnded)
			}
			if (got.PausedTotal == nil) != (tc.wantTotal == nil) || (got.PausedTotal != nil && *got.PausedTotal != *tc.wantTotal) {
				t.Fatalf("total = %v, want %v", got.PausedTotal, tc.wantTotal)
			}
		})
	}
}
