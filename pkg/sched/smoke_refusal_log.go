package sched

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// smokeRefusalLogEvery bounds the diagnostic to one line per deployment per
// interval: a refused smoke is retried every few seconds for minutes.
const smokeRefusalLogEvery = 30 * time.Second

// smokeRefusalThrottle remembers when each deployment's refusal was last
// logged. Entries older than the interval are dropped whenever one is
// claimed, so the map holds only deployments refused in the last window.
type smokeRefusalThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// claim reports whether key may log now, and records it atomically so two
// concurrent refusals of one deployment log once.
func (t *smokeRefusalThrottle) claim(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if at, ok := t.last[key]; ok && now.Sub(at) < smokeRefusalLogEvery {
		return false
	}
	if t.last == nil {
		t.last = make(map[string]time.Time)
	}
	for k, at := range t.last {
		if now.Sub(at) >= smokeRefusalLogEvery {
			delete(t.last, k)
		}
	}
	t.last[key] = now
	return true
}

// logSmokeRefusal records why a deployment's post-readiness smoke could not
// get an instance. The refusal surfaces to the customer as
// deployment_verification_unavailable, and until now nothing said which
// instances held the app's slots or which ceiling refused
// (ADR-20261123120000). It changes no decision.
func (e *Engine) logSmokeRefusal(ctx context.Context, appID, deploymentID string, res WakeResult, err error) {
	if e.log == nil || !e.smokeRefusals.claim(appID+"/"+deploymentID, time.Now()) {
		return
	}
	found := res.admissionBlockers
	if err != nil {
		found = append(append([]api.WorkerAdmissionBlocker(nil), found...), admissionBlockers(err)...)
	}
	blockers := make([]string, 0, len(found))
	for _, b := range found {
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
			// The store fallback returns history; only live rows hold slots.
			if !state.IsLive(ins.State) {
				continue
			}
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
