package vmmdgrpc

// adr: 20261122132055 §7 — a stopping vmmd asks each session's owner to
// move it first: agentd pauses a persistent session (its capture stays on
// this node and resumes after the restart, or elsewhere once uploaded) and
// ends another. vmmd waits a bounded time for the streams to end before it
// stops serving. Until sessions are adopted across restarts this is how a
// restart loses no persistent session.

import (
	"context"
	"log/slog"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// SandboxEvacuateWait bounds how long a stopping vmmd waits for its
// sessions to leave: inside systemd's default 90 s stop timeout, and long
// enough for the largest shape's node-first capture.
const SandboxEvacuateWait = 75 * time.Second

// sandboxEvacuatePoll is how often the wait checks for streams left.
const sandboxEvacuatePoll = 250 * time.Millisecond

// sandboxEvacuateVMStopping is the reason a stopping vmmd gives.
const sandboxEvacuateVMStopping = "vmmd_stopping"

// EvacuateSandboxes asks every live session's owner to move it off this
// vmmd and waits until their streams end, ctx ends or wait passes. It
// returns the sessions still running.
func (s *Server) EvacuateSandboxes(ctx context.Context, wait time.Duration, log *slog.Logger) int {
	asked := 0
	s.sandboxRuns.Range(func(_, v any) bool {
		act := v.(*sandboxActivity)
		if act.detached.Load() {
			// No owner listens; the stop ends it.
			return true
		}
		ev := &vmmdpb.RunSandboxEvent{Frame: &vmmdpb.RunSandboxEvent_Evacuate{Evacuate: &vmmdpb.RunSandboxEvacuate{Reason: sandboxEvacuateVMStopping}}}
		go func() {
			select {
			case act.outbox <- ev:
			case <-act.done:
			case <-ctx.Done():
			}
		}()
		asked++
		return true
	})
	if asked == 0 {
		return 0
	}
	if log != nil {
		log.Info("vmmd: evacuating sandbox sessions before stopping", "sessions", asked, "wait", wait)
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	tick := time.NewTicker(sandboxEvacuatePoll)
	defer tick.Stop()
	for {
		left := s.liveSandboxes()
		if left == 0 {
			return 0
		}
		select {
		case <-ctx.Done():
			return left
		case <-deadline.C:
			if log != nil {
				log.Warn("vmmd: sandbox sessions still running at stop", "sessions", left)
			}
			return left
		case <-tick.C:
		}
	}
}

// liveSandboxes counts the session streams this vmmd serves.
func (s *Server) liveSandboxes() int {
	n := 0
	s.sandboxRuns.Range(func(_, v any) bool {
		if !v.(*sandboxActivity).detached.Load() {
			n++
		}
		return true
	})
	return n
}
