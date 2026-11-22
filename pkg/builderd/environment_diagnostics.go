package builderd

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"time"
)

func (b *Builderd) environmentDiagnostic(ctx context.Context, r state.ExecutionEnvironment, stage, code, message string) error {
	s, ok := b.store.(state.ExecutionEnvironmentDiagnosticStore)
	if !ok {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(ctx, api.ExecutionEnvironmentDiagnosticWriteTimeoutSeconds*time.Second)
	defer cancel()
	return s.AppendExecutionEnvironmentEvent(writeCtx, r, api.ExecutionEnvironmentEvent{Stage: stage, Code: code, Message: message})
}
func (b *Builderd) environmentLogs(ctx context.Context, r state.ExecutionEnvironment, h BuildHandle) func() {
	logs, ok := b.vm.(EnvironmentLogVM)
	if !ok {
		return func() {}
	}
	live, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		count := 0
		err := logs.StreamEnvironmentBuildLogs(live, h, func(line string, gap bool) error {
			if !gap {
				line = runenv.BuildDiagnosticLine(line)
				if line == "" {
					return nil
				}
			}
			count++
			if count > api.ExecutionEnvironmentDiagnosticLiveMaxEvents {
				return errEnvironmentLogLimit
			}
			code := "build_output"
			if gap {
				code = "log_gap"
				line = "Some guest output fell outside the retained live log window."
			}
			return b.environmentDiagnostic(live, r, "build", code, line)
		})
		if errors.Is(err, errEnvironmentLogLimit) {
			_ = b.environmentDiagnostic(live, r, "build", "log_gap", "Live output reached its diagnostic budget; the final build tail will still be retained.")
		} else if err != nil && live.Err() == nil {
			_ = b.environmentDiagnostic(live, r, "build", "logs_unavailable", "Live build output is unavailable; completed-build diagnostics will still be retained.")
		}
	}()
	return func() { cancel(); <-done }
}
func (b *Builderd) environmentLogTail(ctx context.Context, r state.ExecutionEnvironment, raw string) {
	ctx, cancel := context.WithTimeout(ctx, api.ExecutionEnvironmentCleanupSeconds*time.Second)
	defer cancel()
	lines := strings.Split(raw, "\n")
	if len(lines) > api.ExecutionEnvironmentDiagnosticMaxEvents {
		lines = lines[len(lines)-api.ExecutionEnvironmentDiagnosticMaxEvents:]
	}
	for _, line := range lines {
		if safe := runenv.BuildDiagnosticLine(line); safe != "" {
			if err := b.environmentDiagnostic(ctx, r, "build", "build_output", safe); err != nil {
				return
			}
		}
	}
}

var errEnvironmentLogLimit = errors.New("builderd: diagnostic output limit")
