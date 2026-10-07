package buildpublisher

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagepublisher"
)

type FileSigner struct {
	name string
	key  *ecdsa.PrivateKey
}

// NewFileSigner opens one regular private-key descriptor with owner-only
// permissions. Configuring it never enrolls the public key in company trust.
func NewFileSigner(name, path string) (*FileSigner, error) {
	if !boundedLabel(name, false) || path == "" {
		return nil, ErrInvalid
	}
	f, err := os.Open(path) //nolint:forbidigo // Explicit operator key path, never a customer upload; the opened descriptor must be bounded, regular and owner-only before reading.
	if err != nil {
		return nil, ErrInvalid
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > api.ApplicationStandardMaxPublisherKeyBytes {
		return nil, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.ApplicationStandardMaxPublisherKeyBytes+1))
	if err != nil || len(raw) > api.ApplicationStandardMaxPublisherKeyBytes {
		return nil, ErrInvalid
	}
	block, rest := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" || len(rest) != 0 {
		return nil, ErrInvalid
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, ErrInvalid
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, ErrInvalid
	}
	if _, err := imagepublisher.PublisherKeySHA256(&key.PublicKey); err != nil {
		return nil, ErrInvalid
	}
	return &FileSigner{name: name, key: key}, nil
}

func (s *FileSigner) Sign(ctx context.Context, claims Claims) (Proof, error) {
	if s == nil {
		return Proof{}, ErrInvalid
	}
	return Sign(ctx, claims, s.name, s.key)
}
