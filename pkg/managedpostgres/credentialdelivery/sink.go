package credentialdelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

const MaxCredentialBytes = 32 << 10

var databaseDNSLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type SecretStore interface {
	PutManagedPostgresSecret(context.Context, state.AppSecret) error
	DeleteManagedPostgresSecret(context.Context, string) error
}

// Sink is the Gregale side of the provider-neutral
// CredentialSink boundary. Plaintext exists only long enough to construct and
// seal one connection URL; the app-secret store receives ciphertext plus
// non-secret ownership metadata.
type Sink struct {
	store     SecretStore
	recipient func() *age.X25519Recipient
	hmacKey   func() []byte
}

func New(store SecretStore, recipient func() *age.X25519Recipient, hmacKey func() []byte) (*Sink, error) {
	if store == nil || recipient == nil || hmacKey == nil {
		return nil, managedpostgres.ErrInvalid
	}
	return &Sink{store: store, recipient: recipient, hmacKey: hmacKey}, nil
}

func (s *Sink) Put(ctx context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (string, error) {
	sealed, err := s.SealCredential(ctx, binding, material)
	if err != nil {
		return "", err
	}
	if err := s.store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID: binding.AccountID, AppID: binding.AppID, Scope: binding.Scope, Key: binding.EnvironmentKey,
		Ciphertext: sealed.Ciphertext, Kid: sealed.Kid, ValueHash: sealed.ValueHash,
		ManagedPostgresBindingID: binding.ID, ManagedPostgresAccess: string(binding.Access),
		ManagedCredentialRef: sealed.Ref, ManagedCredentialGeneration: binding.CredentialGeneration,
	}); err != nil {
		return "", normalizeCredentialSinkError(err)
	}
	return sealed.Ref, nil
}

// SealCredential prepares an unpublished envelope. Only Put publishes to
// app_secrets; cutover preparation persists the envelope in its staging table.
func (s *Sink) SealCredential(_ context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (managedpostgres.SealedCredential, error) {
	return Seal(binding, material, s.recipient(), s.hmacKey())
}

func Seal(binding managedpostgres.Binding, material managedpostgres.CredentialMaterial, recipient *age.X25519Recipient, hmacKey []byte) (managedpostgres.SealedCredential, error) {
	value, err := ConnectionURL(binding.Access, material)
	if err != nil {
		return managedpostgres.SealedCredential{}, err
	}
	if len(value) > MaxCredentialBytes {
		return managedpostgres.SealedCredential{}, managedpostgres.ErrUnsupported
	}
	if recipient == nil {
		return managedpostgres.SealedCredential{}, managedpostgres.ErrUnavailable
	}
	if len(hmacKey) == 0 {
		return managedpostgres.SealedCredential{}, managedpostgres.ErrUnavailable
	}
	valueHash, err := secretbox.ValueFingerprint([]byte(value), hmacKey)
	if err != nil {
		return managedpostgres.SealedCredential{}, managedpostgres.ErrUnavailable
	}
	ciphertext, err := secretbox.SealOne(recipient, binding.EnvironmentKey, value, MaxCredentialBytes)
	if err != nil {
		return managedpostgres.SealedCredential{}, managedpostgres.ErrUnavailable
	}
	credentialRef, err := Ref(binding)
	if err != nil {
		return managedpostgres.SealedCredential{}, err
	}
	return managedpostgres.SealedCredential{ProviderIdentityID: material.ProviderIdentityID, Ref: credentialRef, Ciphertext: ciphertext, Kid: recipient.String(), ValueHash: valueHash}, nil
}

func (s *Sink) Delete(ctx context.Context, binding managedpostgres.Binding) error {
	currentRef, err := Ref(binding)
	if err != nil {
		return err
	}
	previousRef := ""
	if binding.RotationPreviousGeneration > 0 {
		previous := binding
		previous.CredentialGeneration = binding.RotationPreviousGeneration
		previousRef, err = Ref(previous)
		if err != nil {
			return err
		}
	}
	if binding.CredentialRef != "" && binding.CredentialRef != currentRef && binding.CredentialRef != previousRef {
		return managedpostgres.ErrConflict
	}
	if err := s.store.DeleteManagedPostgresSecret(ctx, currentRef); err != nil {
		return normalizeCredentialSinkError(err)
	}
	if previousRef != "" && previousRef != currentRef {
		if err := s.store.DeleteManagedPostgresSecret(ctx, previousRef); err != nil {
			return normalizeCredentialSinkError(err)
		}
	}
	return nil
}

func Ref(binding managedpostgres.Binding) (string, error) {
	if binding.ID == "" || binding.CredentialGeneration < 1 {
		return "", managedpostgres.ErrInvalid
	}
	sum := sha256.Sum256([]byte(binding.ID + "\x00" + strconv.FormatInt(binding.CredentialGeneration, 10)))
	return "managed-postgres-" + hex.EncodeToString(sum[:]), nil
}

func ConnectionURL(access managedpostgres.CredentialAccess, material managedpostgres.CredentialMaterial) (string, error) {
	if err := material.Validate(); err != nil {
		return "", err
	}
	// A PEM cannot be represented portably inside a libpq connection URL.
	// Fail closed until the binding contract can own a second sealed file or
	// environment variable rather than silently discarding trust material.
	if material.RootCertificatePEM != "" {
		return "", managedpostgres.ErrUnsupported
	}
	endpoint, ok := selectManagedPostgresEndpoint(access, material.Endpoints)
	if !ok || !validManagedPostgresHost(endpoint.Host) {
		return "", managedpostgres.ErrUnsupported
	}
	databasePath := url.PathEscape(material.Database)
	connection := url.URL{
		Scheme:  "postgresql",
		User:    url.UserPassword(material.Username, material.Password),
		Host:    net.JoinHostPort(endpoint.Host, strconv.FormatUint(uint64(endpoint.Port), 10)),
		Path:    "/" + material.Database,
		RawPath: "/" + databasePath,
	}
	query := url.Values{}
	query.Set("sslmode", material.TLSMode)
	connection.RawQuery = query.Encode()
	return connection.String(), nil
}

func selectManagedPostgresEndpoint(access managedpostgres.CredentialAccess, endpoints []managedpostgres.Endpoint) (managedpostgres.Endpoint, bool) {
	if access == managedpostgres.CredentialReadOnly {
		for _, endpoint := range endpoints {
			if endpoint.Role == managedpostgres.EndpointReadOnly {
				return endpoint, true
			}
		}
		// Read-only is enforced by the qualified role's SQL privileges. A
		// replica endpoint is optional; primary pooled/direct connections
		// preserve the same permission contract and read-after-write behavior.
	}
	if access == managedpostgres.CredentialMigration {
		for _, endpoint := range endpoints {
			if endpoint.Role == managedpostgres.EndpointDirect {
				return endpoint, true
			}
		}
		return managedpostgres.Endpoint{}, false
	}
	if access != managedpostgres.CredentialReadWrite && access != managedpostgres.CredentialReadOnly && access != managedpostgres.CredentialDataAPI {
		return managedpostgres.Endpoint{}, false
	}
	for _, preferred := range []managedpostgres.EndpointRole{managedpostgres.EndpointPooled, managedpostgres.EndpointDirect} {
		for _, endpoint := range endpoints {
			if endpoint.Role == preferred {
				return endpoint, true
			}
		}
	}
	return managedpostgres.Endpoint{}, false
}

func validManagedPostgresHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/\\@?#%\x00\r\n\t ") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !databaseDNSLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func normalizeCredentialSinkError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrConflict):
		return managedpostgres.ErrConflict
	case errors.Is(err, state.ErrNotFound):
		return managedpostgres.ErrNotFound
	case errors.Is(err, state.ErrInvalidArgument):
		return managedpostgres.ErrInvalid
	default:
		return managedpostgres.ErrUnavailable
	}
}
