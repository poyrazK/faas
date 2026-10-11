package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

type nativeArtifactRetirementRecoveryFake struct {
	calls  int
	afters []string
	limits []int
	cancel context.CancelFunc
}

func (f *nativeArtifactRetirementRecoveryFake) RecoverNativeQualificationArtifactRetirementPage(ctx context.Context, after string, limit int) (fcvm.NativeQualificationArtifactRetirementPage, error) {
	if _, ok := ctx.Deadline(); !ok {
		return fcvm.NativeQualificationArtifactRetirementPage{}, errors.New("retirement attempt has no deadline")
	}
	f.calls++
	f.afters = append(f.afters, after)
	f.limits = append(f.limits, limit)
	if f.calls == 1 {
		return fcvm.NativeQualificationArtifactRetirementPage{Examined: 1, NextCursor: "00000000-0000-4000-8000-000000000001", More: true}, errors.New("transient storage failure")
	}
	if f.calls == 2 {
		return fcvm.NativeQualificationArtifactRetirementPage{Examined: 1, NextCursor: "00000000-0000-4000-8000-000000000002"}, nil
	}
	f.cancel()
	return fcvm.NativeQualificationArtifactRetirementPage{Examined: 1, NextCursor: "00000000-0000-4000-8000-000000000003"}, nil
}

func TestNativeArtifactRetirementRecoveryRetriesAfterFailureAndStopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	recoverer := &nativeArtifactRetirementRecoveryFake{cancel: cancel}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	done := make(chan struct{})
	go func() {
		runNativeArtifactRetirementRecovery(ctx, recoverer, time.Millisecond, log)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("retirement retry loop did not stop on cancellation")
	}
	if recoverer.calls != 3 {
		t.Fatalf("retirement recovery calls = %d, want pagination, wrap and shutdown", recoverer.calls)
	}
	if len(recoverer.afters) != 3 || recoverer.afters[0] != "" || recoverer.afters[1] != "00000000-0000-4000-8000-000000000001" || recoverer.afters[2] != "" {
		t.Fatalf("retirement recovery cursors = %v, want empty, prior page cursor, then wrap", recoverer.afters)
	}
	if len(recoverer.limits) != 3 || recoverer.limits[0] != api.NativeSnapshotPublicationRecoveryBatchMax || recoverer.limits[1] != api.NativeSnapshotPublicationRecoveryBatchMax || recoverer.limits[2] != api.NativeSnapshotPublicationRecoveryBatchMax {
		t.Fatalf("retirement recovery limits = %v", recoverer.limits)
	}
}
