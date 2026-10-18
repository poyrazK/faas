package internalmail

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type keyringConfig struct {
	LegacyKeyID   string   `json:"legacy_key_id,omitempty"`
	IdentityPaths []string `json:"identity_paths"`
}

// LoadMailCodec never generates or overwrites key material. An explicitly
// configured keyring remains readable with management off for runtime rollback.
func LoadMailCodec(cfg Config) (*KeyringCodec, error) {
	var input keyringConfig
	if cfg.KeyringPath != "" {
		var err error
		input, err = readKeyringConfig(cfg.KeyringPath)
		if err != nil {
			return nil, errors.New("internal mail: invalid private keyring file")
		}
	}
	var identities []*age.X25519Identity
	if input.LegacyKeyID == "" {
		if len(input.IdentityPaths) >= api.InternalMailMaxEncryptionKeys {
			return nil, ErrInvalid
		}
		legacy, err := secretbox.LoadHostKey(cfg.IdentityPath)
		if err != nil {
			return nil, errors.New("internal mail: cannot load legacy encryption identity")
		}
		input.LegacyKeyID = EncryptionKeyID(legacy)
		identities = append(identities, legacy)
	}
	for _, path := range input.IdentityPaths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return nil, ErrInvalid
		}
		identity, err := secretbox.LoadHostKey(path)
		if err != nil {
			return nil, errors.New("internal mail: cannot load encryption identity")
		}
		identities = append(identities, identity)
	}
	return NewKeyringCodec(cfg.Namespace, input.LegacyKeyID, identities)
}

func readKeyringConfig(path string) (keyringConfig, error) {
	f, err := os.Open(path) //nolint:forbidigo // Trusted operator environment path, never an API/customer path; f.Stat checks regular-file type and private mode before any read.
	if err != nil {
		return keyringConfig{}, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || (info.Mode().Perm() != 0400 && (info.Mode().Perm() != 0440 || !strings.Contains(path, "/credentials/"))) {
		return keyringConfig{}, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(f, api.InternalMailMaxKeyringConfigBytes+1))
	if err != nil || len(raw) > api.InternalMailMaxKeyringConfigBytes {
		return keyringConfig{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input keyringConfig
	if decoder.Decode(&input) != nil || len(input.IdentityPaths) > api.InternalMailMaxEncryptionKeys {
		return keyringConfig{}, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return keyringConfig{}, ErrInvalid
	}
	return input, nil
}
