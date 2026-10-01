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
}

func NewCutoverService(bindings *BindingService, store CutoverStore, sealer CredentialSealer) (*CutoverService, error) {
	if bindings == nil || store == nil || sealer == nil {
		return nil, ErrInvalid
	}
	return &CutoverService{bindings: bindings, store: store, sealer: sealer}, nil
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
func (s *CutoverService) Reconcile(ctx context.Context, account, id string) (Cutover, error) {
	c, err := s.store.GetCutover(ctx, account, id)
	if err != nil {
		return Cutover{}, err
	}
	if c.State == CutoverPrepared || c.State == CutoverCancelled {
		return c, nil
	}
	if c.State == CutoverPreparing && (!s.bindings.provisioningEnabled() || !s.bindings.provisioningAllowed(ctx, account)) {
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

// Sweep prepares at most one member per intent, and always discovers cleanup
// even with provisioning disabled. It performs no workload or secret publication.
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
