package agentd

// adr: 20261122132055 §7 — evacuation: schedd (a draining node) or vmmd (a
// stop) asks agentd to move a session off its node. A persistent session
// pauses and asks for its resume at once, which waits for the upload on a
// draining node and for the restart on a stopping vmmd; another session
// ends with node_evacuated.

import (
	"time"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// evacuate moves the session off its node (ADR-20261122132055 §7): a
// persistent one pauses at the next tick and resumes after; another ends.
func (s *Sandboxes) evacuate(ss *session, reason string, send func(*scheddpb.SandboxRunRequest) error) {
	if reason == "" {
		reason = "evacuate"
	}
	if ss.claim.Kind != agentstore.SandboxPersistent {
		if ss.decide(agentstore.SandboxFailed, sandboxOutcomeEvacuated, "the session's node stopped serving sessions ("+reason+")") {
			_ = send(&scheddpb.SandboxRunRequest{Frame: &scheddpb.SandboxRunRequest_Close{Close: &scheddpb.SandboxRunClose{}}})
		}
		return
	}
	ss.mu.Lock()
	first := ss.evacuate == ""
	ss.evacuate = reason
	// An evacuation is not refused for being early.
	ss.pauseRetryAt = time.Time{}
	ss.mu.Unlock()
	if first {
		s.log.Info("agentd: evacuating sandbox", "sandbox_id", ss.claim.ID, "reason", reason)
	}
}
