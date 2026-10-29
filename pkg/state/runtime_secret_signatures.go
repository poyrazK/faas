package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RuntimeSecretSignatureStore keeps the writer signature of each runtime
// secret envelope apid seals, by the envelope's SHA-256 (ADR 20261028161502,
// amendment 1). Every copy of the same bytes shares one signature.
type RuntimeSecretSignatureStore interface {
	// PutRuntimeSecretSignature stores the signature of the envelope whose
	// SHA-256 is digest, replacing a different one.
	PutRuntimeSecretSignature(ctx context.Context, digest, signature []byte) error
	// RuntimeSecretSignatures returns the stored signature of each digest
	// that has one, keyed by the digest's bytes.
	RuntimeSecretSignatures(ctx context.Context, digests [][]byte) (map[string][]byte, error)
}

var (
	_ RuntimeSecretSignatureStore = (*PgStore)(nil)
	_ RuntimeSecretSignatureStore = (*MemStore)(nil)
)

// MaxRuntimeSecretSignatureLookup bounds one RuntimeSecretSignatures call.
const MaxRuntimeSecretSignatureLookup = 4096

func validRuntimeSecretSignature(digest, signature []byte) error {
	if len(digest) != sha256.Size || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("runtime secret signature: %w", ErrInvalidArgument)
	}
	return nil
}

func validRuntimeSecretDigests(digests [][]byte) error {
	if len(digests) > MaxRuntimeSecretSignatureLookup {
		return fmt.Errorf("runtime secret signatures: %d digests: %w", len(digests), ErrInvalidArgument)
	}
	for _, digest := range digests {
		if len(digest) != sha256.Size {
			return fmt.Errorf("runtime secret signatures: %w", ErrInvalidArgument)
		}
	}
	return nil
}

func (s *PgStore) PutRuntimeSecretSignature(ctx context.Context, digest, signature []byte) error {
	if err := validRuntimeSecretSignature(digest, signature); err != nil {
		return err
	}
	if err := sqlc.New().PutRuntimeSecretSignature(ctx, s.pool, sqlc.PutRuntimeSecretSignatureParams{
		CiphertextSha256: digest, Signature: signature,
	}); err != nil {
		return fmt.Errorf("put runtime secret signature: %w", mapErr(err))
	}
	return nil
}

func (s *PgStore) RuntimeSecretSignatures(ctx context.Context, digests [][]byte) (map[string][]byte, error) {
	if err := validRuntimeSecretDigests(digests); err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(digests))
	if len(digests) == 0 {
		return out, nil
	}
	rows, err := sqlc.New().ReadRuntimeSecretSignatures(ctx, s.pool, digests)
	if err != nil {
		return nil, fmt.Errorf("read runtime secret signatures: %w", mapErr(err))
	}
	for _, row := range rows {
		out[string(row.CiphertextSha256)] = row.Signature
	}
	return out, nil
}

func (m *MemStore) PutRuntimeSecretSignature(_ context.Context, digest, signature []byte) error {
	if err := validRuntimeSecretSignature(digest, signature); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.runtimeSecretSignatures == nil {
		m.runtimeSecretSignatures = make(map[string][]byte)
	}
	m.runtimeSecretSignatures[string(digest)] = bytes.Clone(signature)
	return nil
}

func (m *MemStore) RuntimeSecretSignatures(_ context.Context, digests [][]byte) (map[string][]byte, error) {
	if err := validRuntimeSecretDigests(digests); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string][]byte, len(digests))
	for _, digest := range digests {
		if sig, ok := m.runtimeSecretSignatures[string(digest)]; ok {
			out[string(digest)] = bytes.Clone(sig)
		}
	}
	return out, nil
}
