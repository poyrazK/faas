package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routejobs"
	"github.com/onebox-faas/faas/pkg/state"
)

// routeHealthHoldScan bounds the holds read to find one canary's.
const routeHealthHoldScan = 20

// abortRouteHealthHoldPastDeadline aborts a canary the enforce-mode route
// health guard has held for longer than api.RouteHealthHoldDeadline,
// restoring its predecessor like a timed-out approval (ADR-20261028110044).
// It runs on the rollout worker's blocked advance, so the hold is current.
func (s *server) abortRouteHealthHoldPastDeadline(ctx context.Context, err error, app state.App, d state.Deployment) {
	var blocked *state.RouteHealthBlockedError
	if !errors.As(err, &blocked) {
		return
	}
	holds, ok := s.store.(state.RouteAlertStore)
	decisions, okDecisions := s.store.(state.RolloutDecisionStore)
	if !ok || !okDecisions {
		return
	}
	now := time.Now()
	found, _, readErr := holds.RouteHealthHoldsSince(ctx, app.ID, now, routeHealthHoldScan)
	if readErr != nil {
		s.log.Warn("apid: read route health hold failed", "deployment", d.ID, "err", readErr)
		return
	}
	held, past := routejobs.HoldPastDeadline(found, d.ID, now)
	if !past {
		return
	}
	summary := fmt.Sprintf("route health held the canary for %s, past the %s deadline", held.Round(time.Minute), api.RouteHealthHoldDeadline)
	decision, encodeErr := json.Marshal(api.ManualRolloutDecision(api.RolloutRuleAbortRouteHealthTimeout, summary, d.CanaryStep, d.TrafficPercent, now))
	if encodeErr != nil {
		return
	}
	if _, _, abortErr := decisions.RecoverRolloutWithDecision(ctx, app.ID, "abort", "route health: "+summary, decision); abortErr != nil {
		s.log.Warn("apid: abort route health hold failed", "deployment", d.ID, "err", abortErr)
	}
}
