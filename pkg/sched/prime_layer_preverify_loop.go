package sched

import (
	"context"
	"encoding/json"
)

// dispatchLayerVerify runs Engine.PreverifyPrimeLayer off the loop goroutine
// for a layer_published notice. The notice is advisory, so a bad payload, a
// full pool or a failed verification is logged and dropped: the deployment's
// prime verifies the layer as before.
func (l *Loop) dispatchLayerVerify(ctx context.Context, payload string) {
	var p struct {
		AppID        string `json:"app_id"`
		DeploymentID string `json:"deployment_id"`
	}
	if err := json.Unmarshal([]byte(payload), &p); err != nil || p.AppID == "" || p.DeploymentID == "" {
		l.log.Warn("sched: bad layer_published payload", "payload", payload, "err", err)
		return
	}
	l.submitWork(workLayerVerify, p.DeploymentID, func() {
		if err := l.engine.PreverifyPrimeLayer(ctx, p.AppID, p.DeploymentID); err != nil && ctx.Err() == nil {
			l.log.Warn("sched: layer verification before prime failed; the prime verifies again",
				"app", p.AppID, "deployment", p.DeploymentID, "err", err)
		}
	})
}
