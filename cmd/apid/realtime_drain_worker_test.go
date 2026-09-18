package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type failingRealtimeDrainWorker struct {
	err error
}

func (w failingRealtimeDrainWorker) ClaimManagedRealtimeDrainOperations(context.Context, int, time.Duration) ([]state.ManagedRealtimeDrainOperationClaim, error) {
	return nil, w.err
}

func (failingRealtimeDrainWorker) RetryManagedRealtimeDrainOperation(context.Context, string, string, []string, json.RawMessage, int, int, int, time.Time, string) error {
	return nil
}

func (failingRealtimeDrainWorker) FinishManagedRealtimeDrainOperation(context.Context, string, string, state.ManagedRealtimeDrainOperationStatus, json.RawMessage, int, int, int) error {
	return nil
}

func TestManagedRealtimeDrainClaimFailureIsObservable(t *testing.T) {
	ops := wire.NewOpsMetrics("apid_test")
	srv := &server{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ops: ops,
	}
	srv.runManagedRealtimeDrainPass(context.Background(), failingRealtimeDrainWorker{err: errors.New("claim failed")})

	if body := scrapeOpsMetrics(t, ops); !strings.Contains(body, `apid_test_ops_total{code="err",op="managed_realtime_drain_claim"} 1`) {
		t.Fatalf("metrics missing realtime drain claim failure:\n%s", body)
	}
}
