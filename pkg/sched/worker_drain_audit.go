package sched

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) auditWorkerDrain(ctx context.Context, ins state.Instance, deadline time.Time, outcome string) {
	depID, err := uuid.Parse(ins.DeploymentID)
	if err != nil {
		return
	}
	data, _ := json.Marshal(map[string]any{"instance_id": ins.ID, "deadline": deadline.UTC(), "outcome": outcome})
	kind := state.DeployWorkerDrainCompleted
	if outcome == "draining" {
		kind = state.DeployWorkerDrainStarted
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	entry := state.DeploymentAudit{DeploymentID: depID, Kind: kind, Actor: "schedd:worker_drain", At: time.Now().UTC(), Data: data}
	if app, err := e.store.AppByID(auditCtx, ins.AppID); err == nil {
		if accountID, err := uuid.Parse(app.AccountID); err == nil {
			entry.AccountID = &accountID
		}
	}
	if _, err := e.store.AppendDeploymentAudit(auditCtx, entry); err != nil {
		e.log.Warn("sched: record worker drain", "deployment", ins.DeploymentID, "instance", ins.ID, "outcome", outcome, "err", err)
	}
}
