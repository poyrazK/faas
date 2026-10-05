package objectstorage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EncryptionConfig is an explicit provider capability and account-owned key
// allowlist. ProviderKeyID contains a resource identity, never secret material.
type EncryptionConfig struct {
	KMSEndpoint string                 `json:"kms_endpoint,omitempty"`
	Algorithms  []string               `json:"algorithms,omitempty"`
	Keys        []EncryptionKeyBinding `json:"keys,omitempty"`
}

type EncryptionKeyBinding struct {
	ID            string `json:"id"`
	AccountID     string `json:"account_id"`
	ProviderKeyID string `json:"provider_key_id"`
	Reference     string `json:"-"`
	Identity      string `json:"-"`
}

// ResolvedObjectEncryption is private immutable dispatch data. Its native
// identity must not enter a customer DTO or a provider error message.
type ResolvedObjectEncryption = state.ObjectEncryptionSnapshot

// ObjectEncryptionProvider is internal to Gregale. Public routes may use it
// only after durably admitting and capturing the immutable owned selection.
// Direct provider URLs returned here must remain private to the gateway.
type ObjectEncryptionProvider interface {
	CheckEncryptionKey(context.Context, ResolvedObjectEncryption) error
	WriteEncryptedObject(context.Context, string, string, string, io.Reader, int64, ObjectMetadata, ResolvedObjectEncryption) (UploadResult, error)
	PresignEncryptedPut(context.Context, string, SignRequest, ObjectWriteConditions, string, ResolvedObjectEncryption) (SignedRequest, error)
	CopyEncryptedObject(context.Context, string, string, CopyObjectRequest, CopySourceSnapshot, CopySourceConditions, ResolvedObjectEncryption) (CopyObjectResult, error)
	EnsureEncryptedMultipart(context.Context, string, MultipartCreateRequest, ResolvedObjectEncryption) (string, error)
	CompleteEncryptedMultipart(context.Context, string, MultipartCompleteRequest, ObjectWriteConditions, ResolvedObjectEncryption) (MultipartCompletionResult, error)
	ConfirmEncryptedObject(context.Context, string, string, string, int64, ResolvedObjectEncryption) (UploadResult, error)
	ConfirmEncryptedObjectHistory(context.Context, string, ObjectHistoryProofRequest, ResolvedObjectEncryption) (ObjectHistoryProofPage, error)
}

func cloneObjectEncryption(e api.ObjectEncryption) api.ObjectEncryption {
	if e.BucketKeyEnabled != nil {
		value := *e.BucketKeyEnabled
		e.BucketKeyEnabled = &value
	}
	return e
}

func cloneEncryptionConfig(c EncryptionConfig) EncryptionConfig {
	c.Algorithms = slices.Clone(c.Algorithms)
	c.Keys = slices.Clone(c.Keys)
	return c
}

func normalizeEncryptionConfig(c EncryptionConfig, backend BackendConfig, publicRegion string) (EncryptionConfig, error) {
	c = cloneEncryptionConfig(c)
	bad := fmt.Errorf("object storage: backend %s has invalid encryption capabilities or key bindings", backend.ID)
	if len(c.Keys) > api.MaxObjectEncryptionKeys || len(c.Algorithms) > 3 || (backend.Driver != "s3" && backend.Driver != "gcs") && (len(c.Keys) != 0 || len(c.Algorithms) != 0 || c.KMSEndpoint != "") {
		return c, bad
	}
	if c.KMSEndpoint != "" {
		u, err := url.Parse(c.KMSEndpoint)
		if backend.Driver != "s3" || len(c.Keys) == 0 || err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || u.Scheme != "https" && (!backend.AllowHTTP || u.Scheme != "http") {
			return c, bad
		}
	}
	seen := map[string]bool{}
	for _, algorithm := range c.Algorithms {
		if seen[algorithm] || algorithm != "AES256" && algorithm != "aws:kms" && algorithm != "aws:kms:dsse" || backend.Driver == "gcs" && algorithm == "aws:kms:dsse" {
			return c, bad
		}
		seen[algorithm] = true
	}
	if len(c.Keys) != 0 && !seen["aws:kms"] && !seen["aws:kms:dsse"] {
		return c, bad
	}
	refs, native := map[string]bool{}, map[string]bool{}
	for i := range c.Keys {
		key := &c.Keys[i]
		if !canonicalEncryptionUUID(key.ID) || !canonicalEncryptionUUID(key.AccountID) || !validProviderEncryptionKey(backend, key.ProviderKeyID) {
			return c, bad
		}
		key.Reference = fmt.Sprintf("arn:gregale:kms:%s:%s:key/%s", publicRegion, key.AccountID, key.ID)
		if refs[key.Reference] || native[key.ProviderKeyID] {
			return c, bad
		}
		refs[key.Reference], native[key.ProviderKeyID] = true, true
		identity := sha256.Sum256([]byte(fingerprint(backend) + "\x00" + c.KMSEndpoint + "\x00" + key.Reference + "\x00" + key.ProviderKeyID))
		key.Identity = hex.EncodeToString(identity[:])
	}
	return cloneEncryptionConfig(c), nil
}

func canonicalEncryptionUUID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func validProviderEncryptionKey(backend BackendConfig, value string) bool {
	if len(value) > api.MaxObjectEncryptionKeyRefBytes || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	switch backend.Driver {
	case "s3":
		parts := strings.Split(value, ":")
		return len(parts) == 6 && parts[0] == "arn" && slices.Contains([]string{"aws", "aws-cn", "aws-us-gov"}, parts[1]) && parts[2] == "kms" && parts[3] == backend.S3Region && regexp.MustCompile(`^[0-9]{12}$`).MatchString(parts[4]) && regexp.MustCompile(`^key/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|mrk-[0-9a-f]{32})$`).MatchString(parts[5])
	case "gcs":
		parts := strings.Split(value, "/")
		if len(parts) != 8 || parts[0] != "projects" || parts[2] != "locations" || parts[4] != "keyRings" || parts[6] != "cryptoKeys" || parts[3] != strings.ToLower(backend.GCSLocation) {
			return false
		}
		valid := regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
		return valid.MatchString(parts[1]) && valid.MatchString(parts[3]) && valid.MatchString(parts[5]) && valid.MatchString(parts[7])
	default:
		return false
	}
}

func (c EncryptionConfig) Resolve(account string, requested api.ObjectEncryption) (ResolvedObjectEncryption, error) {
	if !requested.Valid() {
		return ResolvedObjectEncryption{}, ErrInvalid
	}
	if requested.Empty() {
		return ResolvedObjectEncryption{}, nil
	}
	if !canonicalEncryptionUUID(account) {
		return ResolvedObjectEncryption{}, ErrInvalid
	}
	requested = cloneObjectEncryption(requested)
	// Explicit KMS writes must not inherit a mutable native bucket-key default.
	if requested.Algorithm == "aws:kms" && requested.BucketKeyEnabled == nil {
		disabled := false
		requested.BucketKeyEnabled = &disabled
	}
	if !slices.Contains(c.Algorithms, requested.Algorithm) {
		return ResolvedObjectEncryption{}, ErrUnsupported
	}
	if requested.Algorithm == "AES256" {
		return ResolvedObjectEncryption{AccountID: account, Selection: requested}, nil
	}
	for _, key := range c.Keys {
		if key.AccountID == account && key.Reference == requested.KeyID {
			return ResolvedObjectEncryption{AccountID: account, Selection: requested, ProviderKeyID: key.ProviderKeyID, KeyIdentity: key.Identity}, nil
		}
	}
	return ResolvedObjectEncryption{}, ErrInvalid
}

func (c EncryptionConfig) VerifySnapshot(account string, snapshot ResolvedObjectEncryption) error {
	resolved, err := c.Resolve(account, snapshot.Selection)
	if err != nil {
		return err
	}
	if resolved.AccountID != snapshot.AccountID || resolved.ProviderKeyID != snapshot.ProviderKeyID || resolved.KeyIdentity != snapshot.KeyIdentity {
		return ErrConfiguration
	}
	if resolved.Selection.BucketKeyEnabled != nil && (snapshot.Selection.BucketKeyEnabled == nil || *resolved.Selection.BucketKeyEnabled != *snapshot.Selection.BucketKeyEnabled) {
		return ErrConfiguration
	}
	return nil
}
