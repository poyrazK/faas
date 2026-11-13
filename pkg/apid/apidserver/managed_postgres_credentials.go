package apidserver

import (
	"context"
	"crypto/subtle"
	"filippo.io/age"
	"fmt"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const managedPostgresCredentialMaxBytes = credentialdelivery.MaxCredentialBytes

type managedPostgresSecretStore = credentialdelivery.SecretStore

type appSecretCredentialSink struct {
	delivery   *credentialdelivery.Sink
	recipient  func() *age.X25519Recipient
	hmacKey    func() []byte
	identities func() []*age.X25519Identity
	probe      func(context.Context, string, managedpostgres.CredentialAccess, int) error
}

func newAppSecretCredentialSink(store managedPostgresSecretStore, recipient func() *age.X25519Recipient, hmacKey func() []byte) (*appSecretCredentialSink, error) {
	delivery, err := credentialdelivery.New(store, recipient, hmacKey)
	if err != nil {
		return nil, err
	}
	delivery.BindAccounts(runtimeSecretBinding)
	return &appSecretCredentialSink{delivery: delivery, recipient: recipient, hmacKey: hmacKey}, nil
}
func (s *appSecretCredentialSink) Put(ctx context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (string, error) {
	return s.delivery.Put(ctx, binding, material)
}
func (s *appSecretCredentialSink) SealCredential(ctx context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (managedpostgres.SealedCredential, error) {
	if s.delivery != nil {
		return s.delivery.SealCredential(ctx, binding, material)
	}
	if s.recipient == nil || s.hmacKey == nil {
		return managedpostgres.SealedCredential{}, managedpostgres.ErrUnavailable
	}
	return credentialdelivery.SealBound(binding, material, s.recipient(), runtimeSecretBinding(binding.AccountID), s.hmacKey())
}
func (s *appSecretCredentialSink) PublishSealed(ctx context.Context, binding managedpostgres.Binding, sealed managedpostgres.SealedCredential) error {
	return s.delivery.PublishSealed(ctx, binding, sealed)
}
func (s *appSecretCredentialSink) Delete(ctx context.Context, binding managedpostgres.Binding) error {
	return s.delivery.Delete(ctx, binding)
}
func managedPostgresCredentialRef(binding managedpostgres.Binding) (string, error) {
	return credentialdelivery.Ref(binding)
}
func managedPostgresConnectionURL(access managedpostgres.CredentialAccess, material managedpostgres.CredentialMaterial) (string, error) {
	return credentialdelivery.ConnectionURL(access, material)
}

func (s *appSecretCredentialSink) seal(ctx context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (state.AppSecret, string, error) {
	sealed, err := s.SealCredential(ctx, binding, material)
	if err != nil {
		return state.AppSecret{}, "", err
	}
	return state.AppSecret{
		AccountID: binding.AccountID, AppID: binding.AppID, Scope: binding.Scope, Key: binding.EnvironmentKey,
		Ciphertext: sealed.Ciphertext, Kid: sealed.Kid, ValueHash: sealed.ValueHash,
		ManagedPostgresBindingID: binding.ID, ManagedPostgresAccess: string(binding.Access),
		ManagedCredentialRef: sealed.Ref, ManagedCredentialGeneration: binding.CredentialGeneration,
	}, sealed.Ref, nil
}

// VerifyCredential decrypts the exact staged envelope; it never publishes or
// requests replacement credentials. Errors contain no connection strings.
func (s *appSecretCredentialSink) VerifyCredential(ctx context.Context, binding managedpostgres.Binding, sealed managedpostgres.SealedCredential, target managedpostgres.Database) error {
	ref, err := managedPostgresCredentialRef(binding)
	if err != nil || ref != sealed.Ref || binding.DatabaseID != target.ID {
		return fmt.Errorf("%w: staged credential does not match the binding or target", managedpostgres.ErrUnavailable)
	}
	if s.identities == nil || s.probe == nil {
		return fmt.Errorf("%w: credential verifier is not configured", managedpostgres.ErrUnavailable)
	}
	var identities []*age.X25519Identity
	for _, identity := range s.identities() {
		if identity != nil && identity.Recipient().String() == sealed.Kid {
			identities = append(identities, identity)
		}
	}
	if len(identities) == 0 {
		return fmt.Errorf("%w: no decryption identity for the staged credential's key", managedpostgres.ErrUnavailable)
	}
	envelope, err := secretbox.OpenMulti(identities, sealed.Ciphertext)
	if err != nil || len(envelope) != 1 {
		return fmt.Errorf("%w: staged credential envelope does not open", managedpostgres.ErrUnavailable)
	}
	value, ok := envelope[binding.EnvironmentKey]
	if !ok || len(value) == 0 || len(value) > managedPostgresCredentialMaxBytes {
		return fmt.Errorf("%w: staged credential envelope has no usable value for its key", managedpostgres.ErrUnavailable)
	}
	fingerprint, err := secretbox.ValueFingerprint([]byte(value), s.hmacKey())
	if err != nil || subtle.ConstantTimeCompare([]byte(fingerprint), []byte(sealed.ValueHash)) != 1 {
		return fmt.Errorf("%w: staged credential fingerprint does not match", managedpostgres.ErrUnavailable)
	}
	// A session login has read_write privileges; only its endpoint differs.
	if err := s.probe(ctx, value, binding.Access.Privileges(), target.Spec.PostgresMajor); err != nil {
		return fmt.Errorf("probe staged credential on the target: %w", err)
	}
	return nil
}
