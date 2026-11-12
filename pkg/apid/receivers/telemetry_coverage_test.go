package receivers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

type coverageReceiverStore struct {
	*state.MemStore
	report state.TelemetryCoverage
	err    error
}

func (s *coverageReceiverStore) RecordTelemetryCoverage(_ context.Context, c state.TelemetryCoverage) error {
	s.report = c
	return s.err
}

func TestTelemetryCoverageReceiver(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		enabled, invalid, missing, failure bool
		code                               codes.Code
	}{
		{"healthy", true, false, false, false, codes.OK},
		{"scoped", true, false, false, false, codes.OK},
		{"bad_app", true, false, false, false, codes.InvalidArgument},
		{"overcount", true, false, false, false, codes.InvalidArgument},
		{"duplicate", true, false, false, false, codes.InvalidArgument},
		{"disabled", false, false, false, false, codes.OK},
		{"invalid", true, true, false, false, codes.InvalidArgument},
		{"missing", true, false, true, false, codes.Unavailable},
		{"write_error", true, false, false, true, codes.Internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &coverageReceiverStore{MemStore: state.NewMemStore()}
			if tc.failure {
				store.err = errors.New("database unavailable")
			}
			receiver := NewRequestTelemetryReceiver(store, nil, nil, tc.enabled)
			if tc.missing {
				receiver.store = store.MemStore
			}
			req := &apidpb.TelemetryCoverage{NodeName: state.DefaultLocalNodeName, BootId: uuid.NewString(), Sequence: 1, Enabled: true, SamplingBasisPoints: 10000, SourceAtUnixMs: time.Now().UnixMilli()}
			if tc.invalid {
				req.BootId = "bad"
			}
			switch tc.name {
			case "scoped", "bad_app", "overcount", "duplicate":
				req.AppScoped = true
				req.PendingCount = 1
				req.AppGaps = []*apidpb.TelemetryAppGap{{AppId: uuid.NewString(), DroppedCount: 1, PendingCount: 1}}
				if tc.name == "bad_app" {
					req.AppGaps[0].AppId = "bad"
				}
				if tc.name == "overcount" {
					req.AppGaps[0].PendingCount = 2
				}
				if tc.name == "duplicate" {
					req.AppGaps = append(req.AppGaps, req.AppGaps[0])
					req.PendingCount = 2
				}
			}
			receipt, err := receiver.RecordTelemetryCoverage(context.Background(), req)
			if status.Code(err) != tc.code {
				t.Fatalf("outcome: %+v %v", receipt, err)
			}
			if tc.name == "scoped" && (!store.report.AppScoped || len(store.report.AppGaps) != 1 || store.report.AppGaps[0].AppID != req.AppGaps[0].AppId) {
				t.Fatalf("app coverage lost: %+v", store.report)
			}
			if tc.code == codes.OK && (receipt == nil || !receipt.Recorded || store.report.Enabled != tc.enabled) {
				t.Fatalf("coverage not persisted: %+v %+v", receipt, store.report)
			}
		})
	}
}

// The analytics part of a heartbeat reaches the store; counting reads as off
// on an apid that keeps no rollups (ADR-20261103113952 amendment 6).
func TestTelemetryCoverageReceiverAnalyticsPart(t *testing.T) {
	app := uuid.NewString()
	for _, rollups := range []bool{true, false} {
		store := &coverageReceiverStore{MemStore: state.NewMemStore()}
		receiver := NewRequestTelemetryReceiver(store, nil, nil, true)
		if rollups {
			NewRequestAnalyticsRollups(&fakeRollupStore{}, nil, nil).applyRequestTelemetry(receiver)
		}
		_, err := receiver.RecordTelemetryCoverage(t.Context(), &apidpb.TelemetryCoverage{
			NodeName: state.DefaultLocalNodeName, BootId: uuid.NewString(), Sequence: 1, Enabled: true, SamplingBasisPoints: 10000,
			SourceAtUnixMs: time.Now().UnixMilli(), AppScoped: true,
			AppGaps:   []*apidpb.TelemetryAppGap{{AppId: app, AnalyticsDroppedCount: 3, AnalyticsPendingCount: 2}},
			Analytics: &apidpb.AnalyticsCoverage{Counting: true, UnattributedDroppedTotal: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		a := store.report.Analytics
		if a == nil || a.Counting != rollups || a.UnattributedDroppedTotal != 1 {
			t.Fatalf("rollups %v: analytics = %+v", rollups, a)
		}
		if g := store.report.AppGaps; len(g) != 1 || g[0].AnalyticsDroppedCount != 3 || g[0].AnalyticsPendingCount != 2 {
			t.Fatalf("gaps = %+v", g)
		}
	}
}
