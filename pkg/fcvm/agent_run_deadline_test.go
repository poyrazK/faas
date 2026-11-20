package fcvm

import (
	"testing"
	"time"
)

// adr: 20261027090016 — amendment 3: a run's deadline grows by its
// approval-wait credit, never past the guest's ceiling.

func agentDeadlineManager(instance string, deadline time.Duration) *Manager {
	m := &Manager{live: map[string]*Instance{instance: {AgentRunOnly: true, AgentRunDeadline: deadline}}}
	return m
}

func closedWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

func TestAgentDeadlineRunStopsAtTheSellerDeadline(t *testing.T) {
	m := agentDeadlineManager("agent-1", 30*time.Millisecond)
	cancel, timedOut, stop := m.agentDeadlineRun("agent-1", 30*time.Millisecond, time.Hour, make(chan struct{}))
	defer stop()
	if !closedWithin(cancel, time.Second) || !timedOut() {
		t.Fatal("the run was not stopped at its deadline")
	}
}

func TestAgentDeadlineRunCreditMovesTheDeadline(t *testing.T) {
	m := agentDeadlineManager("agent-1", 50*time.Millisecond)
	// Credit sent before the clock starts is kept for it.
	m.ExtendAgentRun("agent-1", 150*time.Millisecond)
	cancel, timedOut, stop := m.agentDeadlineRun("agent-1", 50*time.Millisecond, time.Hour, nil)
	defer stop()
	if closedWithin(cancel, 120*time.Millisecond) {
		t.Fatal("credit granted before the start was lost")
	}
	m.ExtendAgentRun("agent-1", 100*time.Millisecond) // smaller: ignored
	m.ExtendAgentRun("agent-1", 300*time.Millisecond)
	if closedWithin(cancel, 150*time.Millisecond) {
		t.Fatal("more credit did not move the deadline")
	}
	if !closedWithin(cancel, 2*time.Second) || !timedOut() {
		t.Fatal("the run outlived its credited deadline")
	}
}

func TestAgentDeadlineRunNeverPassesTheCeiling(t *testing.T) {
	m := agentDeadlineManager("agent-1", 20*time.Millisecond)
	cancel, timedOut, stop := m.agentDeadlineRun("agent-1", 20*time.Millisecond, 60*time.Millisecond, nil)
	defer stop()
	m.ExtendAgentRun("agent-1", time.Hour)
	if !closedWithin(cancel, time.Second) || !timedOut() {
		t.Fatal("credit carried the run past the guest's ceiling")
	}
}

func TestAgentDeadlineRunCallerCancelIsNotATimeout(t *testing.T) {
	m := agentDeadlineManager("agent-1", time.Hour)
	callerCancel := make(chan struct{})
	cancel, timedOut, stop := m.agentDeadlineRun("agent-1", time.Hour, 2*time.Hour, callerCancel)
	defer stop()
	close(callerCancel)
	if !closedWithin(cancel, time.Second) || timedOut() {
		t.Fatal("a caller's cancel was not passed on, or read as a timeout")
	}
}

func TestAgentDeadlineRunWithoutSellerDeadline(t *testing.T) {
	m := agentDeadlineManager("agent-1", 0)
	callerCancel := make(chan struct{})
	cancel, timedOut, stop := m.agentDeadlineRun("agent-1", 0, time.Hour, callerCancel)
	stop()
	if cancel != (<-chan struct{})(callerCancel) || timedOut() {
		t.Fatal("a run without a seller deadline got a vmmd deadline")
	}
	m.ExtendAgentRun("agent-1", time.Minute)
	if len(m.agentDeadlines) != 0 {
		t.Fatalf("credit for a run without a seller deadline was kept: %v", m.agentDeadlines)
	}
}

func TestAgentDeadlineRunCleansUp(t *testing.T) {
	m := agentDeadlineManager("agent-1", time.Hour)
	_, _, stop := m.agentDeadlineRun("agent-1", time.Hour, 2*time.Hour, nil)
	stop()
	stop()
	if len(m.agentDeadlines) != 0 {
		t.Fatalf("deadlines left after stop: %v", m.agentDeadlines)
	}
}
