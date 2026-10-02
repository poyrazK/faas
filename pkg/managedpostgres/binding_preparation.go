package managedpostgres

import (
	"context"
	"time"
)

// PrepareReservedCredential issues a deterministic credential for private,
// already authenticated clone intent. The owning worker must use a lease
// deadline and a sink which commits the sealed envelope and preparation proof
// under that lease. It never invokes the ordinary runtime mutation sink or
// finishes a binding through a separate catalogue write.
func (s *BindingService) PrepareReservedCredential(ctx context.Context, requested Binding, operationID string, sink CredentialSink) (string, string, error) {
	if sink == nil || operationID == "" || requested.ID == "" || requested.CredentialGeneration != 1 {
		return "", "", ErrInvalid
	}
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.After(time.Now()) {
		return "", "", ErrInvalid
	}
	if !s.provisioningEnabled() || !s.provisioningAllowed(ctx, requested.AccountID) {
		return "", "", ErrUnavailable
	}
	binding, err := s.bindings.GetBinding(ctx, requested.AccountID, requested.ID)
	if err != nil {
		return "", "", err
	}
	if binding.DatabaseID != requested.DatabaseID || binding.AppID != requested.AppID || binding.Scope != requested.Scope || binding.EnvironmentKey != requested.EnvironmentKey || binding.Access != requested.Access ||
		binding.CredentialGeneration != 1 || binding.State != BindingStateProvisioning || binding.LeaseToken != "" || binding.ProviderIdentityID != "" || binding.CredentialRef != "" || binding.RotationPreviousGeneration != 0 || binding.RotationWakeID != "" {
		return "", "", ErrConflict
	}
	database, err := s.databases.Get(ctx, binding.AccountID, binding.DatabaseID)
	if err != nil {
		return "", "", err
	}
	if database.EnvironmentCloneOperationID != operationID || database.State != StateReady || database.ProviderResourceID == "" || database.ProviderResourceID == database.RestoreSourceResourceID || !validDataResourceID(database.DataResourceID) || database.DataResourceID == database.RestoreSourceResourceID {
		return "", "", ErrConflict
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return "", "", err
	}
	if err := backend.Capabilities.SupportsCredentialAccess(binding.Access); err != nil {
		return "", "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.providerTimeout)
	material, err := backend.Provider.IssueCredentials(callCtx, bindingCredentialRequest(binding, databaseDataResource(database)))
	cancel()
	defer clearCredentialMaterial(&material)
	if err != nil {
		return "", "", err
	}
	if err := material.Validate(); err != nil {
		return "", "", ErrUnavailable
	}
	ref, err := sink.Put(ctx, binding, material)
	if err != nil {
		return "", "", err
	}
	if !validOpaqueID(ref) {
		return "", "", ErrConflict
	}
	return material.ProviderIdentityID, ref, nil
}
