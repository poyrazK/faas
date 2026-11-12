package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Restore identity (ADR-20261031193635 slice 5b). An environment branch that
// opts in with after_restore.identity: reload restores from its parent's
// credential-free capture. Restored memory still holds the capture boot's
// placeholder configuration, so after the restore guest-init fetches the
// branch's own env and secrets from vmmd, writes them to a tmpfs file and
// calls the after_restore hook with that file's path and digest. The hook
// must reload its configuration and echo the digest; anything else counts as
// after_restore_failed, and vmmd cold-boots the branch instead.

const (
	// RestoreIdentityFilePath is the guest tmpfs file holding the branch's
	// identity document. Mode 0400, owned by the application user.
	RestoreIdentityFilePath = "/tmp/gregale-identity/identity.json"
	// AfterRestoreIdentityFileHeader names the identity file in the
	// after_restore request.
	AfterRestoreIdentityFileHeader = "X-Faas-Identity-File"
	// AfterRestoreIdentityDigestHeader carries the document's digest in the
	// after_restore request.
	AfterRestoreIdentityDigestHeader = "X-Faas-Identity-Digest"
	// AfterRestoreIdentityAckHeader is the hook's answer: the digest of the
	// document it reloaded.
	AfterRestoreIdentityAckHeader = "X-Faas-Identity-Ack"
	// RuntimeConfigKindIdentity is the runtime-config request that returns
	// the restored instance's identity document.
	RuntimeConfigKindIdentity = "identity"
)

// RestoreIdentity is the document the after_restore hook reloads: the
// branch's plain env and its secrets, managed binding URLs included.
type RestoreIdentity struct {
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}

// RestoreIdentityDocument encodes the identity and returns its digest, the
// lowercase hex SHA-256 of the encoded bytes. Map keys encode sorted, so
// vmmd and guest-init derive the same digest from the same values.
func RestoreIdentityDocument(env, secrets map[string]string) ([]byte, string, error) {
	doc := RestoreIdentity{Env: env, Secrets: secrets}
	if doc.Env == nil {
		doc.Env = map[string]string{}
	}
	if doc.Secrets == nil {
		doc.Secrets = map[string]string{}
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return nil, "", fmt.Errorf("encode restore identity: %w", err)
	}
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:]), nil
}

// ValidRestoreIdentityDigest reports whether digest has the form
// RestoreIdentityDocument returns.
func ValidRestoreIdentityDigest(digest string) bool {
	if len(digest) != sha256.Size*2 {
		return false
	}
	for _, c := range digest {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
