package imaged

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) beginHostingVerification(ctx context.Context, app state.App, dep state.Deployment, started time.Time) (*state.HostingVerificationProgress, error) {
	store, ok := h.store.(state.DeploymentHostingVerificationStore)
	if !ok || h.hostingSmoke == nil {
		return nil, nil
	}
	p, err := store.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationBegin, At: h.hostingVerificationTime()})
	if errors.Is(err, state.ErrHostingVerificationExpired) {
		return nil, h.hostingVerificationUnavailable(ctx, app, dep, started, p.LastRouteChecks)
	}
	if errors.Is(err, state.ErrHostingVerificationFinalized) {
		return nil, errHostingVerificationFinalized
	}
	if err != nil {
		return nil, fmt.Errorf("imaged: begin hosting verification: %w", err)
	}
	return &p, nil
}

func (h *Handler) retryHostingVerification(ctx context.Context, app state.App, dep state.Deployment, progress *state.HostingVerificationProgress, started time.Time, code string, cause error, routeChecks *apihostingreceipt.RouteCheckSet) error {
	store, ok := h.store.(state.DeploymentHostingVerificationStore)
	if !ok || progress == nil {
		return fmt.Errorf("imaged: durable hosting verification store unavailable: %w", cause)
	}
	now := h.hostingVerificationTime()
	p, err := store.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{
		Action: state.HostingVerificationRetry, Attempt: progress.Attempts, At: now, ErrorCode: code,
		RouteChecks: routeChecks, RetryNotBefore: now.Add(db.NotificationRetryDelay(progress.Attempts)),
	})
	if errors.Is(err, state.ErrHostingVerificationFinalized) {
		return errHostingVerificationFinalized
	}
	if err != nil {
		return fmt.Errorf("imaged: persist hosting verification retry: %w", err)
	}
	if !now.Before(p.DeadlineAt) {
		return h.hostingVerificationUnavailable(ctx, app, dep, started, p.LastRouteChecks)
	}
	return fmt.Errorf("imaged: hosting verification retry eligible after %s: %w", p.RetryNotBefore.Format(time.RFC3339Nano), cause)
}

func (h *Handler) completeHostingVerification(ctx context.Context, dep state.Deployment, progress *state.HostingVerificationProgress) error {
	if progress == nil {
		return nil
	}
	store := h.store.(state.DeploymentHostingVerificationStore)
	_, err := store.UpdateDeploymentHostingVerification(ctx, dep.ID, state.HostingVerificationUpdate{Action: state.HostingVerificationComplete, Attempt: progress.Attempts, At: h.hostingVerificationTime()})
	if errors.Is(err, state.ErrHostingVerificationFinalized) {
		return errHostingVerificationFinalized
	}
	if err != nil {
		return fmt.Errorf("imaged: complete hosting verification: %w", err)
	}
	return nil
}

func (h *Handler) hostingVerificationTime() time.Time {
	if h.hostingVerificationNow != nil {
		return h.hostingVerificationNow().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) hostingVerificationUnavailable(ctx context.Context, app state.App, dep state.Deployment, started time.Time, routeChecks *apihostingreceipt.RouteCheckSet) error {
	err := errors.New("public candidate verification remained unavailable within the recovery window; inspect deployment and gateway diagnostics, then retry the deployment")
	smoke := apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, Path: HostingHealthPath(app, dep), Authentication: apihostingreceipt.AuthenticationPlatformChallenge, ErrorCode: apihostingreceipt.SmokeErrorVerificationUnavailable, Error: err.Error()}
	if routeChecks != nil {
		copied := *routeChecks
		copied.Status = apihostingreceipt.RouteCheckSetUnavailable
		copied.Checks = append([]apihostingreceipt.RouteCheckResult(nil), routeChecks.Checks...)
		smoke.RouteChecks = &copied
	}
	return h.commitHostingFailure(ctx, app, dep, smoke, err, started)
}
