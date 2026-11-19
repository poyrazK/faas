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
	if crash, ok := h.candidateCrashLoop(ctx, app, dep, routeChecks); ok {
		return h.commitHostingFailure(ctx, app, dep, crash, errors.New(crash.Error), started)
	}
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

// candidateCrashLoop is the app's own verdict for a verification the public
// route could not prove: schedd parked the candidate for liveness exhaustion
// because its process kept exiting. The gateway then answers the probe with a
// bare 502 or a refused connection, which proves nothing about the candidate,
// so without this the deploy waited out the recovery window and failed as
// "verification unavailable", sending its owner to gateway diagnostics instead
// of the app's logs (agent eval 2026-11-18: a Flask app whose gunicorn
// workers could not import their module). A failed read keeps the outage
// path: it is no evidence either way.
func (h *Handler) candidateCrashLoop(ctx context.Context, app state.App, dep state.Deployment, routeChecks *apihostingreceipt.RouteCheckSet) (apihostingreceipt.SmokeResult, bool) {
	current, err := h.store.DeploymentByID(ctx, dep.ID)
	if err != nil || current.ParkedReason != string(state.ParkReasonLivenessExhausted) {
		return apihostingreceipt.SmokeResult{}, false
	}
	detail := "the app's process kept exiting after it started, so the platform stopped restarting it"
	if current.LivenessRestartCount > 0 {
		detail = fmt.Sprintf("the app's process exited %d times after it started, so the platform stopped restarting it", current.LivenessRestartCount)
	}
	detail += fmt.Sprintf("; read why with: gregale logs %s --deployment %s", app.Slug, dep.ID)
	smoke := apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeFailed, Path: HostingHealthPath(app, dep),
		Authentication: apihostingreceipt.AuthenticationPlatformChallenge, ErrorCode: apihostingreceipt.SmokeErrorCandidateCrashLoop, Error: detail}
	if routeChecks != nil {
		copied := *routeChecks
		copied.Checks = append([]apihostingreceipt.RouteCheckResult(nil), routeChecks.Checks...)
		smoke.RouteChecks = &copied
	}
	return smoke, true
}
