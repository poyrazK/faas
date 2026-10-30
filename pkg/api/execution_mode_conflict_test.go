package api_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestExecutionModeConflictDetailRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		conflict  api.ExecutionModeConflict
		listening bool
	}{
		{api.ExecutionModeConflict{Mode: api.ExecutionModeWorker, Class: "http", Port: 3000, ExitCode: -1}, true},
		{api.ExecutionModeConflict{Mode: api.ExecutionModeWorker, Class: "grpc", Port: 50051, ExitCode: -1}, true},
		{api.ExecutionModeConflict{Mode: api.ExecutionModeWorker, Class: "job", ExitCode: 0}, false},
		{api.ExecutionModeConflict{Mode: api.ExecutionModeJob, Class: "http", Port: 8080, ExitCode: -1}, false},
	} {
		wrapped := errors.New("sched: prime: cold boot: execution_mode_conflict: " + tc.conflict.Detail())
		got, ok := api.ParseExecutionModeConflict(wrapped.Error())
		if !ok || got != tc.conflict {
			t.Fatalf("parse %q = %+v, %t; want %+v", wrapped, got, ok, tc.conflict)
		}
		if got.ListeningWorker() != tc.listening {
			t.Fatalf("%+v listening worker = %t, want %t", got, got.ListeningWorker(), tc.listening)
		}
	}
	if _, ok := api.ParseExecutionModeConflict("app_startup_timeout: no listener"); ok {
		t.Fatal("parsed a conflict from unrelated text")
	}
}
