// Package runfilestore streams independently encrypted Run file objects.
// Keys and storage paths are internal; callers enforce account/principal access.
package runfilestore

import (
	"encoding/json"
	"errors"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runfile"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

const KeyNamespace = "execution_file_key"

type keyEnvelope struct {
	Version    int    `json:"version"`
	AccountID  string `json:"account_id"`
	FileID     string `json:"file_id"`
	StorageKey string `json:"storage_key"`
	Identity   string `json:"identity"`
}

func NewKey(accountID, id, key string, recipients []*age.X25519Recipient) (string, []byte, error) {
	if err := runfile.ValidateKey(accountID, id, key); err != nil {
		return "", nil, err
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		return "", nil, errors.New("run file key generation failed")
	}
	plain, err := json.Marshal(keyEnvelope{1, accountID, id, key, identity.String()})
	if err != nil {
		return "", nil, err
	}
	defer clear(plain)
	sealed, err := secretbox.SealBytesTo(recipients, KeyNamespace, plain, api.ExecutionFileKeyMaxBytes)
	if err != nil {
		return "", nil, errors.New("run file key sealing unavailable")
	}
	return identity.String(), sealed, nil
}

func OpenKey(accountID, id, key string, sealed []byte, identities []*age.X25519Identity) (string, error) {
	if err := runfile.ValidateKey(accountID, id, key); err != nil {
		return "", err
	}
	if len(sealed) == 0 || len(sealed) > api.ExecutionFileKeyMaxBytes {
		return "", errors.New("run file key is unavailable")
	}
	namespace, plain, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil {
		return "", errors.New("run file key could not be opened")
	}
	defer clear(plain)
	var envelope keyEnvelope
	if namespace != KeyNamespace || json.Unmarshal(plain, &envelope) != nil || envelope.Version != 1 || envelope.AccountID != accountID || envelope.FileID != id || envelope.StorageKey != key {
		return "", errors.New("run file key scope differs from metadata")
	}
	if _, err := fileIdentity(accountID, runfile.Ref{Descriptor: runfile.Descriptor{ID: id}, StorageKey: key, PrivateKey: envelope.Identity}); err != nil {
		return "", errors.New("run file key is malformed")
	}
	return envelope.Identity, nil
}

// fileIdentity parses a per-object data key that was unwrapped from the Account
// domain or delegated in an authenticated Delivery payload. It never opens an
// identity file, environment variable, fleet key, host key or domain key.
func fileIdentity(accountID string, ref runfile.Ref) (*age.X25519Identity, error) {
	if err := runfile.ValidateKey(accountID, ref.ID, ref.StorageKey); err != nil {
		return nil, err
	}
	if len(ref.PrivateKey) == 0 || len(ref.PrivateKey) > api.ExecutionFileKeyMaxBytes {
		return nil, errors.New("run file key is unavailable")
	}
	identity, err := age.ParseX25519Identity(ref.PrivateKey)
	if err != nil {
		return nil, errors.New("run file key is malformed")
	}
	return identity, nil
}

// ValidateRef checks scoped object-key material before any guest dispatch.
func ValidateRef(accountID string, ref runfile.Ref) error {
	_, err := fileIdentity(accountID, ref)
	return err
}
