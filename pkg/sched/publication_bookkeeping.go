// adr: 1151 — a wake's bookkeeping commits with its RUNNING publication.
package sched

import (
	"context"
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// WithNotifyInPublication sends a wake's instance_changed notification from
// the RUNNING publication's own transaction instead of after it. It requires
// the engine's notifier to use the store's Postgres database, as schedd's
// PoolNotifier does: the notification is then delivered exactly when the
// publication commits, and costs no round trip.
func (e *Engine) WithNotifyInPublication() *Engine {
	if e == nil {
		return e
	}
	e.notifyInPublication = true
	return e
}

// publicationBookkeeping is what a wake commits with its publication of
// instanceID moving from to to: the transition event, and the
// instance_changed notification when the store can send it.
func (e *Engine) publicationBookkeeping(instanceID, appID, wakeID string, from, to state.State, kind string) *state.RuntimePublicationBookkeeping {
	b := &state.RuntimePublicationBookkeeping{EventKind: kind, EventData: instanceTransitionEventData(from, to, "")}
	if payload := instanceChangedPayload(instanceID, appID, to, wakeID); e.notif != nil && e.notifyInPublication && len(payload) <= db.NotifyPayloadMaxBytes {
		b.NotifyChannel, b.NotifyPayload = db.NotifyInstanceChanged, payload
	}
	return b
}

// recordPublishedInstanceTransition finishes a transition whose publication
// committed b: it sends the notification b did not carry, reconciles, and
// clears the deployment's snapshot-miss backoff only when one is recorded.
// The transition event committed with the publication.
func (e *Engine) recordPublishedInstanceTransition(ctx context.Context, ins state.Instance, to state.State, appID, deploymentID string, b *state.RuntimePublicationBookkeeping) {
	if b.NotifyChannel == "" {
		e.emitInstanceChanged(ctx, ins.ID, appID, to, ins.WakeID)
	}
	e.reconcileAfterInstanceTransition(ctx, ins, to)
	if b.SnapshotBackoffRecorded {
		e.clearSnapshotBackoffAfterWake(ctx, deploymentID)
	}
}

// instanceTransitionEventData is the data of schedd's transition event.
func instanceTransitionEventData(from, to state.State, reason string) []byte {
	data, _ := json.Marshal(map[string]any{
		"from": string(from), "to": string(to), "reason": reason, "ts": time.Now().UTC().Format(time.RFC3339Nano),
	})
	return data
}

// instanceChangedPayload is the instance_changed notification. wakeID is the
// per-wake correlation handle; its key is always present (even when empty) so
// subscribers parse one shape. Job-task instances have no app row and say so,
// so gateway subscribers can ignore them (issue #2763).
func instanceChangedPayload(instanceID, appID string, st state.State, wakeID string, nodeIDs ...string) string {
	fields := map[string]any{
		"instance_id": instanceID,
		"app_id":      appID,
		"state":       string(st),
		"wake_id":     wakeID,
	}
	if len(nodeIDs) > 0 {
		fields["node_id"] = nodeIDs[0]
	}
	if appID == "" {
		fields["kind"] = "job"
	}
	payload, _ := json.Marshal(fields)
	return string(payload)
}
