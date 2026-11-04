// adr: 944
package durable

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/durableentity/validatorbundle"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/state"
)

func validatorBundleHash(b validatorbundle.Bundle) string { return validatorbundle.Hash(b) }
func loadDurableEntityValidatorBundles(path string) (map[string]validatorbundle.Bundle, error) {
	return validatorbundle.Load(path)
}

func restoreIsolationUnavailable() *api.Problem {
	return api.NewProblem(http.StatusServiceUnavailable, "durable_entity_restore_isolation_unavailable", "Isolated validation unavailable", "a registered validator bundle and disposable execution runtime are required")
}

func (s *Service) enqueueIsolatedRestoreValidator(ctx context.Context, acct state.Account, app state.App, deploymentID, pin string, body json.RawMessage) (json.RawMessage, string, error) {
	b, bundleErr := s.resolveDurableEntityValidatorBundle(ctx, app.ID, deploymentID)
	if bundleErr != nil || !s.executionAPIEnabled() {
		return nil, "", restoreIsolationUnavailable()
	}
	if pin != "" && pin != b.SHA256 {
		return nil, "", api.ErrValidation("validation_bundle_sha256 does not match the registered deployment bundle")
	}
	request := api.CreateExecutionRequest{Runtime: b.Runtime, Entrypoint: b.Entrypoint, Files: b.Files, Input: body, Network: &api.ExecutionNetworkPolicy{Mode: api.ExecutionNetworkNone}, Limits: &api.ExecutionLimitRequest{TimeoutMS: int(api.DurableEntityRestoreIsolationTimeout / time.Millisecond), MaxOutputBytes: api.MaxDurableEntityRestoreValidationBytes}}
	resolved, problem := request.Resolve(acct.Plan)
	if problem != nil {
		return nil, "", problem
	}
	recipients, kid, err := s.Host.ExecutionPayloadRecipients()
	if err != nil {
		return nil, "", restoreIsolationUnavailable()
	}
	sealed, err := executionpayload.SealRequestTo(recipients, resolved)
	if err != nil {
		return nil, "", restoreIsolationUnavailable()
	}
	now := time.Now().UTC()
	deadline := now.Add(api.DurableEntityRestoreIsolationTimeout)
	if parent, ok := ctx.Deadline(); ok && parent.Before(deadline) {
		deadline = parent
	}
	row, err := s.Host.Store().CreateExecution(ctx, state.CreateExecutionParams{AccountID: acct.ID, Request: resolved, SourceBytes: resolved.SourceBytes(), InputBytes: len(body), AdmittedAt: now, DeadlineAt: deadline, SealedPayload: sealed, PayloadKID: kid})
	if err != nil {
		return nil, "", err
	}
	result, err := s.waitIsolatedRestoreValidator(ctx, acct.ID, row.ID, deadline)
	return result, b.SHA256, err
}

func (s *Service) waitIsolatedRestoreValidator(ctx context.Context, accountID, executionID string, deadline time.Time) (json.RawMessage, error) {
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	ticker := time.NewTicker(api.DurableEntityResultPollInterval)
	defer ticker.Stop()
	for {
		row, err := s.Host.Store().ExecutionByID(ctx, accountID, executionID)
		if err == nil && row.Status.Terminal() {
			if row.Status != api.ExecutionStatusSucceeded || row.OutputTruncated {
				return nil, api.NewProblem(http.StatusBadGateway, "durable_entity_restore_validator_failed", "Validator failed", "isolated execution did not produce a complete successful verdict")
			}
			return row.Result, nil
		}
		if err != nil && ctx.Err() == nil {
			s.cancelRestoreValidator(ctx, accountID, executionID)
			return nil, err
		}
		select {
		case <-ctx.Done():
			s.cancelRestoreValidator(ctx, accountID, executionID)
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) cancelRestoreValidator(parent context.Context, accountID, executionID string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), api.DurableEntityReleaseTimeout)
	defer cancel()
	_, _ = s.Host.Store().RequestExecutionCancellation(ctx, accountID, executionID, time.Now().UTC())
}

func (s *Service) resolveDurableEntityValidatorBundle(ctx context.Context, appID, deploymentID string) (validatorbundle.Bundle, error) {
	if s.ValidatorArtifacts != nil {
		return s.ValidatorArtifacts.Resolve(ctx, appID, deploymentID)
	}
	b, ok := s.ValidatorBundles[deploymentID]
	if !ok || b.AppID != appID || b.DeploymentID != deploymentID || validatorbundle.Validate(b) != nil {
		return b, validatorbundle.ErrArtifactUnavailable
	}
	return b, nil
}
