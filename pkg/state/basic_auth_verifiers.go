package state

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// BasicAuthVerifierStore keeps the verify-only form of apps' basic-auth
// credentials (ADR-20261110210000 slice 3h), keyed by the SHA-256 of the
// sealed blob in apps.public_auth_basic (basicverifier.Key). apid writes;
// gateways read. PgStore and MemStore implement it.
type BasicAuthVerifierStore interface {
	// PutAppBasicAuthVerifier records the verifier of one sealed blob; a
	// repeat write keeps the first.
	PutAppBasicAuthVerifier(ctx context.Context, sealedSHA256 []byte, verifier string) error
	// AppBasicAuthVerifier returns the verifier of one sealed blob, or
	// ErrNotFound.
	AppBasicAuthVerifier(ctx context.Context, sealedSHA256 []byte) (string, error)
}

var errBasicAuthVerifierKey = errors.New("state: basic auth verifier key must be a 32-byte digest")

// PutAppBasicAuthVerifier implements BasicAuthVerifierStore.
func (s *PgStore) PutAppBasicAuthVerifier(ctx context.Context, sealedSHA256 []byte, verifier string) error {
	if len(sealedSHA256) != 32 {
		return errBasicAuthVerifierKey
	}
	if err := sqlc.New().PutAppBasicAuthVerifier(ctx, s.pool, sqlc.PutAppBasicAuthVerifierParams{
		SealedSha256: sealedSHA256, Verifier: verifier,
	}); err != nil {
		return fmt.Errorf("state: put basic auth verifier: %w", err)
	}
	return nil
}

// AppBasicAuthVerifier implements BasicAuthVerifierStore.
func (s *PgStore) AppBasicAuthVerifier(ctx context.Context, sealedSHA256 []byte) (string, error) {
	if len(sealedSHA256) != 32 {
		return "", errBasicAuthVerifierKey
	}
	v, err := sqlc.New().AppBasicAuthVerifier(ctx, s.pool, sealedSHA256)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: basic auth verifier: %w", err)
	}
	return v, nil
}

// PutAppBasicAuthVerifier implements BasicAuthVerifierStore.
func (m *MemStore) PutAppBasicAuthVerifier(_ context.Context, sealedSHA256 []byte, verifier string) error {
	if len(sealedSHA256) != 32 {
		return errBasicAuthVerifierKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.basicAuthVerifiers == nil {
		m.basicAuthVerifiers = map[string]string{}
	}
	key := hex.EncodeToString(sealedSHA256)
	if _, ok := m.basicAuthVerifiers[key]; !ok {
		m.basicAuthVerifiers[key] = verifier
	}
	return nil
}

// AppBasicAuthVerifier implements BasicAuthVerifierStore.
func (m *MemStore) AppBasicAuthVerifier(_ context.Context, sealedSHA256 []byte) (string, error) {
	if len(sealedSHA256) != 32 {
		return "", errBasicAuthVerifierKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.basicAuthVerifiers[hex.EncodeToString(sealedSHA256)]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

var (
	_ BasicAuthVerifierStore = (*PgStore)(nil)
	_ BasicAuthVerifierStore = (*MemStore)(nil)
)
