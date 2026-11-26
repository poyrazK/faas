package managedpostgres

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/dataapiinspect"
	"github.com/onebox-faas/faas/pkg/dataapiinspectioncommand"
)

// DataAPIBindingTarget describes the private provider-derived connection
// coordinates for one binding generation. It is not a connection observation
// or evidence of credential delivery. Do not put these fields in API output.
type DataAPIBindingTarget struct {
	Host              string `json:"-"`
	Port              uint16 `json:"-"`
	Database          string `json:"-"`
	Login             string `json:"-"`
	TrustBundleSHA256 string `json:"-"`
}

// DataAPIBindingTargetProvider resolves expectations without issuing,
// changing, or returning credentials. Only a provider that can establish
// these coordinates from its own resource and binding identity implements it.
type DataAPIBindingTargetProvider interface {
	DataAPIBindingTarget(context.Context, CredentialRequest, string, int64) (DataAPIBindingTarget, error)
}

// ValidDataAPIBindingCredentialRequest ties a provider request to the
// platform's deterministic identity for exactly one binding generation.
func ValidDataAPIBindingCredentialRequest(req CredentialRequest, generation int64) bool {
	return generation > 0 && req.BindingID != "" && req.Access == CredentialDataAPI && req.IdentityKey == bindingCredentialIdentity(Binding{ID: req.BindingID, CredentialGeneration: generation})
}

// DataAPIBindingExpectation is a private, point-in-time control-plane
// snapshot. A consumer must separately authenticate the task connection and
// fence the binding, database and deployment through the observation. It must
// never treat matching these values alone as verified binding identity.
type DataAPIBindingExpectation struct {
	Target                     DataAPIBindingTarget `json:"-"`
	BindingID                  string               `json:"-"`
	DatabaseID                 string               `json:"-"`
	AppID                      string               `json:"-"`
	CredentialGeneration       int64                `json:"-"`
	CredentialRef              string               `json:"-"`
	ProviderIdentityID         string               `json:"-"`
	DatabaseResourceID         string               `json:"-"`
	BackendID                  string               `json:"-"`
	BackendFingerprint         string               `json:"-"`
	BindingUpdatedAt           time.Time            `json:"-"`
	DatabaseUpdatedAt          time.Time            `json:"-"`
	DatabaseDesiredGeneration  int64                `json:"-"`
	DatabaseObservedGeneration int64                `json:"-"`
}

// MatchesObservedTarget compares coordinates only. A match is not proof that
// an authorized guest produced the observation or that the binding and
// deployment remained stable; the caller must independently fence both.
func (e DataAPIBindingExpectation) MatchesObservedTarget(nonce, observed string) (bool, error) {
	decoded, err := hex.DecodeString(observed)
	if err != nil || len(decoded) != 32 || observed != strings.ToLower(observed) {
		return false, ErrInvalid
	}
	expected, err := dataapiinspect.TargetDigest(nonce, e.Target.Host, e.Target.Port, e.Target.Database, e.Target.Login)
	if err != nil {
		return false, ErrInvalid
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(observed)) == 1, nil
}

// MatchesObservedTrust compares a host-supplied bundle fingerprint against the
// provider's configured trust. A caller must also authenticate the task output
// and fence the provider configuration, binding, and deployment.
func (e DataAPIBindingExpectation) MatchesObservedTrust(observed string) (bool, error) {
	if !dataapiinspectioncommand.ValidTrustBundleFingerprint(e.Target.TrustBundleSHA256) || !dataapiinspectioncommand.ValidTrustBundleFingerprint(observed) {
		return false, ErrInvalid
	}
	return subtle.ConstantTimeCompare([]byte(e.Target.TrustBundleSHA256), []byte(observed)) == 1, nil
}

// DataAPIBindingExpectation returns provider-specific expectations for an
// existing selected binding. The scope is intentionally the hosted inspection
// scope; other access modes and providers remain unsupported.
func (s *BindingService) DataAPIBindingExpectation(ctx context.Context, accountID, appID, databaseID, bindingID string) (DataAPIBindingExpectation, error) {
	var result DataAPIBindingExpectation
	if accountID == "" || appID == "" || databaseID == "" || bindingID == "" {
		return result, ErrInvalid
	}
	binding, err := s.Get(ctx, accountID, bindingID)
	if err != nil {
		return result, err
	}
	if binding.ID != bindingID || binding.AppID != appID || binding.DatabaseID != databaseID || binding.Scope != "default" || binding.EnvironmentKey != "DATABASE_URL" || binding.Access != CredentialDataAPI || binding.State != BindingStateReady || binding.CredentialGeneration < 1 || binding.RotationPreviousGeneration != 0 || binding.ProviderIdentityID == "" || binding.CredentialRef == "" {
		return result, ErrConflict
	}
	database, err := customerDatabase(ctx, s.databases, binding.DatabaseNamespace(), binding.DatabaseID)
	if err != nil {
		return result, err
	}
	resource := databaseDataResource(database)
	if database.ID != databaseID || database.State != StateReady || database.DesiredGeneration < 1 || database.ObservedGeneration != database.DesiredGeneration || resource == "" || database.BackendFingerprint == "" {
		return result, ErrConflict
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return result, err
	}
	if backend.Capabilities.SupportsCredentialAccess(CredentialDataAPI) != nil {
		return result, ErrUnsupported
	}
	provider, ok := backend.Provider.(DataAPIBindingTargetProvider)
	if !ok {
		return result, ErrUnsupported
	}
	providerContext, cancel := context.WithTimeout(ctx, s.providerTimeout)
	defer cancel()
	target, err := provider.DataAPIBindingTarget(providerContext, bindingCredentialRequest(binding, resource), binding.ProviderIdentityID, binding.CredentialGeneration)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, normalizeProviderError(err)
	}
	if providerContext.Err() != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, ErrUnavailable
	}
	if target.Host == "" || target.Port == 0 || target.Database == "" || target.Login == "" {
		return result, ErrUnavailable
	}
	// Catch changes while the provider resolved its expectation. This does not
	// replace an atomic fence around any future task observation.
	afterBinding, err := s.Get(ctx, accountID, bindingID)
	if err != nil || afterBinding != binding {
		return result, ErrConflict
	}
	afterDatabase, err := customerDatabase(ctx, s.databases, binding.DatabaseNamespace(), binding.DatabaseID)
	if err != nil || afterDatabase.ID != database.ID || afterDatabase.State != database.State || afterDatabase.BackendID != database.BackendID || afterDatabase.BackendFingerprint != database.BackendFingerprint || afterDatabase.DesiredGeneration != database.DesiredGeneration || afterDatabase.ObservedGeneration != database.ObservedGeneration || databaseDataResource(afterDatabase) != resource || !afterDatabase.UpdatedAt.Equal(database.UpdatedAt) {
		return result, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return DataAPIBindingExpectation{Target: target, BindingID: binding.ID, DatabaseID: database.ID, AppID: binding.AppID, CredentialGeneration: binding.CredentialGeneration, CredentialRef: binding.CredentialRef, ProviderIdentityID: binding.ProviderIdentityID, DatabaseResourceID: resource, BackendID: database.BackendID, BackendFingerprint: database.BackendFingerprint, BindingUpdatedAt: binding.UpdatedAt, DatabaseUpdatedAt: database.UpdatedAt, DatabaseDesiredGeneration: database.DesiredGeneration, DatabaseObservedGeneration: database.ObservedGeneration}, nil
}
