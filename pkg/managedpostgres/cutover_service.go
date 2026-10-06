package managedpostgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type CutoverService struct {
	bindings *BindingService
	store    CutoverStore
	sealer   CredentialSealer
	verifier CredentialVerifier
}

func NewCutoverService(bindings *BindingService, store CutoverStore, sealer CredentialSealer) (*CutoverService, error) {
	if bindings == nil || store == nil || sealer == nil {
		return nil, ErrInvalid
	}
	verifier, _ := sealer.(CredentialVerifier)
	return &CutoverService{bindings: bindings, store: store, sealer: sealer, verifier: verifier}, nil
}
func (s *CutoverService) Prepare(ctx context.Context, r PrepareCutoverRequest) (Cutover, error) {
	if !s.bindings.provisioningEnabled() || !s.bindings.provisioningAllowed(ctx, r.AccountID) {
		return Cutover{}, ErrUnavailable
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if !validPrepareCutover(r, s.bindings.now()) {
		return Cutover{}, ErrInvalid
	}
	target, err := s.bindings.databases.Get(ctx, r.AccountID, r.TargetDatabaseID)
	if err != nil {
		return Cutover{}, err
	}
	backend, err := s.bindings.registry.Resolve(target.BackendID, target.BackendFingerprint)
	if err != nil {
		return Cutover{}, err
	}
	members, err := s.bindings.bindings.ListBindings(ctx, r.AccountID, r.SourceDatabaseID)
	if err != nil {
		return Cutover{}, err
	}
	for _, member := range members {
		if member.AppID == r.AppID && member.Scope == r.Scope {
			if err := backend.Capabilities.SupportsCredentialAccess(member.Access); err != nil {
				return Cutover{}, err
			}
		}
	}
	c, _, err := s.store.ReserveCutover(ctx, r, s.bindings.now())
	if err != nil {
		return Cutover{}, err
	}
	// Reservation persists every member before any provider credential is issued.
	return c, nil
}
func (s *CutoverService) Get(ctx context.Context, account, id string) (Cutover, error) {
	return s.store.GetCutover(ctx, account, id)
}
func (s *CutoverService) Cancel(ctx context.Context, account, id string) (Cutover, error) {
	return s.store.CancelCutover(ctx, account, id, s.bindings.now())
}

// Verify queues durable, read-only verification of every staged credential.
func (s *CutoverService) Verify(ctx context.Context, account, id string) (Cutover, error) {
	if s.verifier == nil || !s.bindings.provisioningEnabled() || !s.bindings.provisioningAllowed(ctx, account) {
		return Cutover{}, ErrUnavailable
	}
	return s.store.RequestCutoverVerification(ctx, account, id, s.bindings.now())
}
func (s *CutoverService) Reconcile(ctx context.Context, account, id string) (Cutover, error) {
	c, err := s.store.GetCutover(ctx, account, id)
	if err != nil {
		return Cutover{}, err
	}
	if c.State == CutoverPrepared || c.State == CutoverVerified || c.State == CutoverCancelled {
		return c, nil
	}
	if c.State != CutoverCancelling && (!s.bindings.provisioningEnabled() || !s.bindings.provisioningAllowed(ctx, account)) {
		return c, ErrUnavailable
	}
	now := s.bindings.now()
	c, err = s.store.ClaimCutover(ctx, account, id, uuid.NewString(), now, now.Add(s.bindings.leaseDuration))
	if err != nil {
		return Cutover{}, err
	}
	backend, err := s.bindings.registry.Resolve(c.Target.BackendID, c.Target.BackendFingerprint)
	if err != nil {
		return c, s.release(ctx, c, "backend_unavailable", ErrUnavailable)
	}
	if c.State == CutoverVerifying {
		return s.verifyClaim(ctx, c)
	}
	for _, member := range c.Credentials {
		if c.State == CutoverPreparing && member.State != "pending" {
			continue
		}
		if c.State == CutoverCancelling && member.State == "revoked" {
			continue
		}
		b := cutoverBinding(c, member)
		request := bindingCredentialRequest(b, c.Target.ProviderResourceID)
		providerCtx, cancel := context.WithTimeout(ctx, s.bindings.providerTimeout)
		if c.State == CutoverCancelling {
			err = backend.Provider.RevokeCredentials(providerCtx, request)
			cancel()
			if err != nil && !errors.Is(err, ErrNotFound) {
				return c, s.release(ctx, c, "credential_revoke_failed", err)
			}
			if ctx.Err() != nil {
				return c, ctx.Err()
			}
			err = s.store.RevokeCutoverCredential(ctx, c, member, s.bindings.now())
		} else {
			if err = backend.Capabilities.SupportsCredentialAccess(member.Access); err != nil {
				cancel()
				return c, s.release(ctx, c, "credential_access_unsupported", err)
			}
			var material CredentialMaterial
			material, err = backend.Provider.IssueCredentials(providerCtx, request)
			cancel()
			if err != nil {
				return c, s.release(ctx, c, "credential_issue_failed", err)
			}
			if err = material.Validate(); err != nil {
				clearCredentialMaterial(&material)
				return c, s.release(ctx, c, "credential_invalid", ErrUnavailable)
			}
			sealed, sealErr := s.sealer.SealCredential(ctx, b, material)
			clearCredentialMaterial(&material)
			if sealErr != nil {
				return c, s.release(ctx, c, "credential_seal_failed", sealErr)
			}
			if ctx.Err() != nil {
				return c, ctx.Err()
			}
			err = s.store.SaveCutoverCredential(ctx, c, member, sealed, s.bindings.now())
		}
		if err != nil {
			return c, err
		}
		return s.store.GetCutover(ctx, account, id)
	}
	return c, ErrConflict
}

func (s *CutoverService) verifyClaim(ctx context.Context, c Cutover) (Cutover, error) {
	if s.verifier == nil {
		return c, s.release(ctx, c, "credential_verifier_unavailable", ErrUnavailable)
	}
	target, err := s.bindings.databases.Get(ctx, c.AccountID, c.Target.ID)
	if err != nil {
		return c, s.release(ctx, c, "verification_target_unavailable", err)
	}
	if target.State != StateReady || target.BackendID != c.Target.BackendID || target.BackendFingerprint != c.Target.BackendFingerprint || target.ProviderResourceID != c.Target.ProviderResourceID || target.DesiredGeneration != c.Target.DesiredGeneration {
		return c, s.release(ctx, c, "verification_target_changed", ErrConflict)
	}
	// Bound the whole batch below the lease, including decrypt and connection I/O.
	probe, cancel := context.WithTimeout(ctx, min(s.bindings.providerTimeout, s.bindings.leaseDuration/2))
	defer cancel()
	for _, m := range c.Credentials {
		if !m.VerifiedAt.IsZero() && !m.VerifiedAt.After(s.bindings.now()) && s.bindings.now().Sub(m.VerifiedAt) <= CutoverVerificationMaxAge {
			continue
		}
		err := s.verifier.VerifyCredential(probe, cutoverBinding(c, m), m.Sealed, target)
		if err == nil {
			err = probe.Err()
		}
		if err != nil {
			return c, s.release(ctx, c, "credential_verification_failed", err)
		}
		if ctx.Err() != nil {
			return c, ctx.Err()
		}
		if err := s.store.SaveCutoverVerification(ctx, c, m, s.bindings.now()); err != nil {
			return c, err
		}
		current, err := s.store.GetCutover(ctx, c.AccountID, c.ID)
		if err != nil || current.State == CutoverVerified {
			return current, err
		}
	}
	return c, s.release(ctx, c, "verification_evidence_expired", ErrConflict)
}
func (s *CutoverService) release(ctx context.Context, c Cutover, code string, cause error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	normalized := normalizeProviderError(cause)
	now := s.bindings.now()
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultStoreTimeout)
	defer cancel()
	if err := s.store.ReleaseCutover(finish, c, code, now, now.Add(retryDelay(normalized, c.AttemptCount))); err != nil {
		return errors.Join(normalized, err)
	}
	return normalized
}

// Sweep stages one member or verifies a bounded batch per intent. Cleanup is
// always discovered, even with provisioning disabled. No secrets are published.
func (s *CutoverService) Sweep(ctx context.Context, limit int) error {
	rows, err := s.store.DueCutovers(ctx, s.bindings.provisioningEnabled(), limit, s.bindings.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, c := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_, err = s.Reconcile(ctx, c.AccountID, c.ID)
		if err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrUnavailable) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
