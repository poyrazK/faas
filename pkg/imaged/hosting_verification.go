package imaged

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

var errHostingVerificationFinalized = errors.New("hosting verification superseded by another transition")

func (h *Handler) verifyHostingCandidate(ctx context.Context, app state.App, dep state.Deployment, required bool, started time.Time) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hosting verification interrupted: %w", err)
	}
	smoke := apihostingreceipt.SmokeResult{Status: apihostingreceipt.SmokeSkipped, Path: HostingHealthPath(app, dep), ErrorCode: apihostingreceipt.SmokeErrorNotConfigured}
	var smokeErr error
	if h.hostingSmoke != nil {
		smoke, smokeErr = h.hostingSmoke(ctx, app, dep)
	}
	// Parent cancellation belongs to the consumer, not the candidate. In
	// particular a shutdown must not turn an interrupted probe into a verdict.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("hosting verification interrupted: %w", err)
	}
	if smokeErr == nil && required && smoke.Status != apihostingreceipt.SmokeVerified {
		smoke.Status = apihostingreceipt.SmokeFailed
		if smoke.ErrorCode == "" || h.hostingSmoke == nil {
			smoke.ErrorCode = apihostingreceipt.SmokeErrorVerifierNotConfigured
		}
		if smoke.Error == "" {
			smoke.Error = "public hosting smoke verifier did not verify deployment"
			if h.hostingSmoke == nil {
				smoke.Error = "public hosting smoke verifier is required but not configured"
			}
		}
	}
	if smokeErr == nil {
		smokeErr = hostingSmokeFailure(smoke)
	}
	if smokeErr != nil {
		smoke.Status = apihostingreceipt.SmokeFailed
		if smoke.Error == "" {
			smoke.Error = smokeErr.Error()
		}
		return h.commitHostingFailure(ctx, app, dep, smoke, smokeErr, started)
	}
	// A failed evidence write remains retryable through snapshot_written. It
	// cannot turn a successfully verified candidate into a terminal failure.
	if err := h.persistHostingReceipt(ctx, app, dep, smoke); err != nil {
		return fmt.Errorf("imaged: hosting receipt: %w", err)
	}
	h.observeHostingVerification(app, wire.APIHostingOutcomeComplete, started)
	return nil
}

func (h *Handler) commitHostingFailure(ctx context.Context, app state.App, dep state.Deployment, smoke apihostingreceipt.SmokeResult, smokeErr error, started time.Time) error {
	store, ok := h.store.(state.DeploymentHostingFailureStore)
	if !ok {
		return fmt.Errorf("imaged: atomic hosting failure store is not configured")
	}
	raw, err := apihostingreceipt.Encode(buildHostingReceipt(app, dep, smoke))
	if err != nil {
		return fmt.Errorf("imaged: encode hosting failure: %w", err)
	}
	changed, err := store.FailDeploymentWithHostingReceipt(ctx, dep.ID, raw, api.CodeDeploymentSmokeFailed, "post-readiness smoke failed: "+smokeErr.Error())
	if err != nil {
		return fmt.Errorf("imaged: commit hosting failure: %w", err)
	}
	if !changed {
		// A concurrent cancellation, promotion or earlier finalizer won. Its
		// terminal evidence and lifecycle events belong to that transition.
		return errHostingVerificationFinalized
	}
	h.observeHostingVerification(app, wire.APIHostingOutcomeFailed, started)
	h.notifyDeploymentState(ctx, dep.AppID, dep.ID, state.DeployFailed)
	return fmt.Errorf("imaged: post-readiness smoke: %w", smokeErr)
}

func (h *Handler) observeHostingVerification(app state.App, outcome string, started time.Time) {
	if h.ops != nil {
		h.ops.ObserveAPIHostingPhase(hostingFlowForApp(app), "verified_url", outcome, time.Since(started))
	}
}
