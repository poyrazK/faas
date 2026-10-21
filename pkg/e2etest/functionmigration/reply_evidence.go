package functionmigration

// adr: 1577

import (
	"errors"
	"maps"
	"slices"
)

func (r Run) checkReplyEvidence() error {
	if err := r.checkCalls(); err != nil {
		return err
	}
	if RestartScenario(r.Scenario) {
		if r.Fault != nil {
			return errors.New("restart case cannot substitute a reply fault")
		}
		return r.checkRestartEvidence()
	}
	if r.Restart != nil {
		return errors.New("reply case cannot claim a coordinator restart")
	}
	if r.Scenario == Success {
		if r.Fault != nil {
			return errors.New("baseline cannot claim an injected fault")
		}
		return nil
	}
	f := r.Fault
	if f == nil {
		return errors.New("reply-boundary case omitted its actual injection")
	}
	if r.Scenario == LostCommit {
		if f.Mode != "post-commit-error" || f.CommitDiscards != 1 || f.RecoveredDecisions != 1 || f.ReadinessDiscards != 0 || f.Replays != nil {
			return errors.New("ownership commit ambiguity was not resolved from durable state")
		}
		return nil
	}
	copies, discards, mode := 3, 0, "explicit-concurrent-retry"
	if r.Scenario == LostReadiness {
		copies, discards, mode = 1, 1, "client-reply-discard"
	} else if r.Scenario != Duplicate {
		return errors.New("unsupported migration reply case")
	}
	if f.Mode != mode || f.ReadinessDiscards != discards || f.CommitDiscards != 0 || f.RecoveredDecisions != 0 || f.Replays == nil ||
		f.OwnerAfterReplays != r.Destination || !f.SourceKeptAfterReplay || !f.ClosedAfterReplay || !SameSessions(r.DestinationSQL, f.SQLAfterReplays) {
		return errors.New("adoption retry changed physical ownership, SQL or pre-commit admission")
	}
	p := f.Replays
	if !hexBytes(p.RequestSHA, 32) || !hexBytes(p.ReplySHA, 32) || len(p.Requests) != copies || len(p.Replies) != copies {
		return errors.New("adoption retry omitted exact request/reply fingerprints")
	}
	for i := range copies {
		if p.Requests[i] != p.RequestSHA || p.Replies[i] != p.ReplySHA {
			return errors.New("adoption retry used another request or physical result")
		}
	}
	return nil
}

// SameSessions compares PostgreSQL backend generations, including the worker
// identity and TLS witness. Ordering does not grant a new pool identity.
func SameSessions(a, b []Session) bool {
	if len(a) != 8 || len(b) != 8 {
		return false
	}
	seen := make(map[Session]bool, len(a))
	for _, session := range a {
		seen[session] = true
	}
	if len(seen) != 8 {
		return false
	}
	for _, session := range b {
		if !seen[session] {
			return false
		}
		delete(seen, session)
	}
	return len(seen) == 0
}

func (r Run) checkCalls() error {
	workers := map[int]bool{}
	for _, w := range r.CheckpointWorkers {
		workers[w.PID] = true
	}
	groups := []map[int]Calls{{}, {}, {}}
	for i, calls := range [][]Calls{r.CallsBefore, r.CallsAtReady, r.CallsAfter} {
		for _, c := range calls {
			if !workers[c.PID] || groups[i][c.PID].PID != 0 {
				return errors.New("resource callback count has an unknown or duplicate interpreter")
			}
			groups[i][c.PID] = c
		}
	}
	if !slices.Contains(Scenarios(), r.Scenario) || len(groups[1]) != 4 || !maps.Equal(groups[1], groups[2]) {
		return errors.New("resource callbacks replayed or have missing observations")
	}
	for pid := range workers {
		before, ready := groups[0][pid], groups[1][pid]
		if ready.Checkpoints <= before.Checkpoints || ready.Checkpoints-before.Checkpoints != 1 || ready.Recoveries <= before.Recoveries || ready.Recoveries-before.Recoveries != 1 {
			return errors.New("every interpreter must checkpoint and recover exactly once in this attempt")
		}
	}
	return nil
}
