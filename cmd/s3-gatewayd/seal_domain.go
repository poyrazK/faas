package main

import (
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
)

// envSealDomainStorageIdentity names the storage seal-domain identity (ADR
// 20261022111812), delivered as a LoadCredential drop-in by the seal_domains
// role. s3-gatewayd is the only reader of Gregale S3 secret access keys.
const envSealDomainStorageIdentity = "FAAS_SEAL_DOMAIN_STORAGE_IDENTITY_PATH"

// loadStorageSealDomain loads the storage seal-domain identity and its
// previous twin once the role stages them; nil before.
func loadStorageSealDomain(getenv func(string) string) ([]*age.X25519Identity, error) {
	path := getenv(envSealDomainStorageIdentity)
	if path == "" {
		return nil, nil
	}
	ids, err := sealdomain.LoadReaderIdentities(path)
	if err != nil {
		return nil, fmt.Errorf("s3-gatewayd: load storage seal-domain identity: %w", err)
	}
	return ids, nil
}

// storageReaders returns the identities that open S3 credentials: the
// storage domain first, then legacy (the fleet and host identities) for
// credentials sealed before the domain, each once.
func storageReaders(storage, legacy []*age.X25519Identity) []*age.X25519Identity {
	out := make([]*age.X25519Identity, 0, len(storage)+len(legacy))
	seen := map[string]bool{}
	for _, id := range append(append([]*age.X25519Identity(nil), storage...), legacy...) {
		if id == nil || seen[id.Recipient().String()] {
			continue
		}
		seen[id.Recipient().String()] = true
		out = append(out, id)
	}
	return out
}
