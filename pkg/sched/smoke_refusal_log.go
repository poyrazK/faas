package sched

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// smokeRefusalLogEvery bounds the diagnostic to one line per deployment per
// interval: a refused smoke is retried every few seconds for minutes.
const smokeRefusalLogEvery = 30 * time.Second

var smokeRefusalLogged sync.Map // appID+"/"+deploymentID -> time.Time

// logSmokeRefusal records why a deployment's post-readiness smoke could not
// get an instance. The refusal surfaces to the customer as
// deployment_verification_unavailable, and until now nothing said which
// instances held the app's slots or which ceiling refused (MCP redeploys,
// 2026-11-21 and 2026-11-23: 1 in 4 back-to-back redeploys of an idle
// max_concurrency=1 app). It changes no decision.
func (e *Engine) logSmokeRefusal(ctx context.Context, appID, deploymentID string, res WakeResult, err error) {
	if e.log == nil {
		return
	}
	key := appID + "/" + deploymentID
	now := time.Now()
	if last, ok := smokeRefusalLogged.Load(key); ok && now.Sub(last.(time.Time)) < smokeRefusalLogEvery {
		return
	}
	smokeRefusalLogged.Store(key, now)

	blockers := make([]string, 0, len(res.admissionBlockers))
	for _, b := range res.admissionBlockers {
		s := b.Code
		if b.Resource != "" {
			s += ":" + b.Resource
		}
		if b.Used != nil && b.Limit != nil {
			s += fmt.Sprintf("=%d/%d", *b.Used, *b.Limit)
		}
		blockers = append(blockers, s)
	}
	holders := []string{}
	if instances, listErr := e.liveInstancesForApp(ctx, appID); listErr == nil {
		for _, ins := range instances {
			holders = append(holders, fmt.Sprintf("%s:%s:%s:%s", shortID(ins.ID), shortID(ins.DeploymentID), ins.State, shortID(ins.NodeID)))
		}
	}
	attrs := []any{
		"app", appID, "deployment", deploymentID,
		"at_capacity", res.AtCapacity, "blockers", strings.Join(blockers, ","),
		"live_instances", strings.Join(holders, ","),
	}
	if e.ledger != nil {
		attrs = append(attrs,
			"ledger_app_concurrency", e.ledger.Concurrency(appID),
			"ledger_production_concurrency", e.ledger.servingEnvironmentConcurrency(appID, "", true))
	}
	if err != nil {
		attrs = append(attrs, "err", err.Error())
	}
	e.log.Warn("sched: deployment smoke refused", attrs...)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
