package main

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

const managedPostgresCredentialMaxBytes = 32 << 10

var databaseDNSLabel = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

type managedPostgresSecretStore interface {
	PutManagedPostgresSecret(context.Context, state.AppSecret) error
	DeleteManagedPostgresSecret(context.Context, string) error
}

// appSecretCredentialSink is the Gregale side of the provider-neutral
// CredentialSink boundary. Plaintext exists only long enough to construct and
// seal one connection URL; the app-secret store receives ciphertext plus
// non-secret ownership metadata.
type appSecretCredentialSink struct {
	store     managedPostgresSecretStore
	recipient func() *age.X25519Recipient
	hmacKey   func() []byte
}

func newAppSecretCredentialSink(store managedPostgresSecretStore, recipient func() *age.X25519Recipient, hmacKey func() []byte) (*appSecretCredentialSink, error) {
	if store == nil || recipient == nil || hmacKey == nil {
		return nil, managedpostgres.ErrInvalid
	}
	return &appSecretCredentialSink{store: store, recipient: recipient, hmacKey: hmacKey}, nil
}

func (s *appSecretCredentialSink) Put(ctx context.Context, binding managedpostgres.Binding, material managedpostgres.CredentialMaterial) (string, error) {
	value, err := managedPostgresConnectionURL(binding.Access, material)
	if err != nil {
		return "", err
	}
	if len(value) > managedPostgresCredentialMaxBytes {
		return "", managedpostgres.ErrUnsupported
	}
	recipient := s.recipient()
	if recipient == nil {
		return "", managedpostgres.ErrUnavailable
	}
	hmacKey := s.hmacKey()
	if len(hmacKey) == 0 {
		return "", managedpostgres.ErrUnavailable
	}
	valueHash, err := secretbox.ValueFingerprint([]byte(value), hmacKey)
	if err != nil {
		return "", managedpostgres.ErrUnavailable
	}
	ciphertext, err := secretbox.SealOne(recipient, binding.EnvironmentKey, value, managedPostgresCredentialMaxBytes)
	if err != nil {
		return "", managedpostgres.ErrUnavailable
	}
	credentialRef, err := managedPostgresCredentialRef(binding)
	if err != nil {
		return "", err
	}
	if err := s.store.PutManagedPostgresSecret(ctx, state.AppSecret{
		AccountID:                   binding.AccountID,
		AppID:                       binding.AppID,
		Scope:                       binding.Scope,
		Key:                         binding.EnvironmentKey,
		Ciphertext:                  ciphertext,
		Kid:                         recipient.String(),
		ValueHash:                   valueHash,
		ManagedPostgresBindingID:    binding.ID,
		ManagedCredentialRef:        credentialRef,
		ManagedCredentialGeneration: binding.CredentialGeneration,
	}); err != nil {
		return "", normalizeCredentialSinkError(err)
	}
	return credentialRef, nil
}

func (s *appSecretCredentialSink) Delete(ctx context.Context, binding managedpostgres.Binding) error {
	expected, err := managedPostgresCredentialRef(binding)
	if err != nil {
		return err
	}
	if binding.CredentialRef != "" && binding.CredentialRef != expected {
		return managedpostgres.ErrConflict
	}
	return normalizeCredentialSinkError(s.store.DeleteManagedPostgresSecret(ctx, expected))
}

func managedPostgresCredentialRef(binding managedpostgres.Binding) (string, error) {
	if binding.ID == "" || binding.CredentialGeneration < 1 {
		return "", managedpostgres.ErrInvalid
	}
	sum := sha256.Sum256([]byte(binding.ID + "\x00" + strconv.FormatInt(binding.CredentialGeneration, 10)))
	return "managed-postgres-" + hex.EncodeToString(sum[:]), nil
}

func managedPostgresConnectionURL(access managedpostgres.CredentialAccess, material managedpostgres.CredentialMaterial) (string, error) {
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
		return managedpostgres.Endpoint{}, false
	}
	if access != managedpostgres.CredentialReadWrite {
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
