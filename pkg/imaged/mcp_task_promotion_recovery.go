package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

type mcpTaskRecoveryContextKey struct{}

var errMCPRecoveryInactive = errors.New("imaged: MCP recovery candidate is inactive")

// Recovery is serialized with notification handling by Loop.Run. The durable
// lease and deployment activation lock also serialize independent daemons.
func (l *Loop) recoverMCPTaskPromotions(ctx context.Context) {
	if l == nil || l.handler == nil || l.store == nil {
		return
	}
	cycleCtx, cancelCycle := context.WithTimeout(ctx, time.Minute)
	defer cancelCycle()
	obsolete, err := l.store.ReconcileObsoleteMCPTaskHolds(cycleCtx, l.handler.nodeName)
	for _, dep := range obsolete {
		l.handler.notifyDeploymentState(ctx, dep.AppID, dep.ID, state.DeploySuperseded)
	}
	if err != nil {
		l.log.Warn("imaged: reconcile obsolete MCP holds", "err", err)
		return
	}
	for i := 0; i < 4 && cycleCtx.Err() == nil; i++ {
		work, err := l.store.ClaimMCPTaskPromotionRecovery(cycleCtx, l.handler.nodeName)
		if err != nil {
			l.log.Warn("imaged: claim MCP promotion recovery", "err", err)
			return
		}
		if work == nil {
			return
		}
		attemptCtx, cancel := context.WithTimeout(cycleCtx, time.Minute)
		err = l.retryMCPTaskPromotion(attemptCtx, work)
		cancel()
		if err == nil {
			current, readErr := l.store.DeploymentByID(ctx, work.DeploymentID)
			if readErr != nil {
				err = readErr
			} else if current.Status != state.DeployLive {
				if current.Status.IsTerminal() {
					err = errMCPRecoveryInactive
				} else {
					err = errors.New("imaged: MCP activation has not completed")
				}
			}
		}
		complete := err == nil || errors.Is(err, errMCPRecoveryInactive)
		outcome := "recovered"
		if errors.Is(err, errMCPRecoveryInactive) {
			outcome = "inactive"
		}
		if errors.Is(err, state.ErrMCPTaskCompatibilityGate) {
			outcome = "held"
		} else if err != nil && !complete {
			outcome = "retry"
		}
		finished, finishErr := l.store.FinishMCPTaskPromotionRecovery(ctx, work.DeploymentID, work.Generation, outcome)
		if finishErr != nil {
			l.log.Warn("imaged: finish MCP promotion recovery", "deployment", work.DeploymentID, "err", finishErr)
		} else if finished {
			if l.mcpRecoveryAttempts != nil {
				l.mcpRecoveryAttempts.WithLabelValues(outcome).Inc()
			}
			if err == nil && l.mcpHoldDuration != nil {
				l.mcpHoldDuration.Observe(time.Since(work.HeldAt).Seconds())
			}
		}
		if err != nil && !complete && !errors.Is(err, state.ErrMCPTaskCompatibilityGate) {
			l.log.Warn("imaged: MCP promotion recovery deferred", "deployment", work.DeploymentID, "err", err)
		}
	}
}

func (l *Loop) retryMCPTaskPromotion(ctx context.Context, work *state.MCPTaskPromotionRecovery) error {
	dep, err := l.store.DeploymentByID(ctx, work.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return errMCPRecoveryInactive
	}
	if err != nil {
		return err
	}
	if dep.Status.IsTerminal() || dep.DeletedAt != nil || dep.Status == state.DeployLive {
		return errMCPRecoveryInactive
	}
	// Avoid expensive smoke when observer evidence is stale or incompatible.
	// The activation transaction checks the report again before switching traffic.
	hold := func(cause error) error {
		detail := api.ErrMCPTaskCompatibilityGate(cause.Error()).Detail
		if err := l.store.RecordMCPTaskPromotionHold(ctx, dep.ID, detail, *work); err != nil {
			return fmt.Errorf("persist MCP recovery hold: %w", err)
		}
		return cause
	}
	report, err := l.store.GetMCPTaskCompatibilityReport(ctx, dep.AppID)
	if err != nil {
		return hold(fmt.Errorf("%w: observer report unavailable", state.ErrMCPTaskCompatibilityGate))
	}
	age := time.Since(report.ObservedAt)
	if age > 45*time.Second || age < -5*time.Second {
		return hold(fmt.Errorf("%w: observer report is stale", state.ErrMCPTaskCompatibilityGate))
	}
	if err := state.CheckMCPTaskCompatibilityReport(report, dep.ID); err != nil {
		return hold(err)
	}
	ctx = context.WithValue(ctx, mcpTaskRecoveryContextKey{}, true)
	switch work.Channel {
	case db.NotifySnapshotWritten:
		var p snapshotWrittenPayload
		if err := json.Unmarshal(work.Payload, &p); err != nil {
			return err
		}
		if p.DeploymentID != dep.ID {
			return state.ErrInvalidArgument
		}
		return l.handler.handleSnapshotWritten(ctx, p)
	case db.NotifyDeploymentReady:
		var p deploymentReadyPayload
		if err := json.Unmarshal(work.Payload, &p); err != nil {
			return err
		}
		if p.DeploymentID != dep.ID {
			return state.ErrInvalidArgument
		}
		return l.handler.handleDeploymentReady(ctx, p)
	default:
		return state.ErrInvalidArgument
	}
}
