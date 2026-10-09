package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
			receiver := newRequestTelemetryReceiver(store, nil, nil, tc.enabled)
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
