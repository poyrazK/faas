package objectstorage

import (
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// PublicCapabilities lists enrollment only, not key health or effective native
// permissions. No discovery request calls KMS or exposes provider identities.
func (c EncryptionConfig) PublicCapabilities(account string) api.ObjectEncryptionCapabilities {
	out := api.ObjectEncryptionCapabilities{Algorithms: []string{}, KeyIDs: []string{}}
	id, err := uuid.Parse(account)
	if err != nil || id == uuid.Nil {
		return out
	}
	out.Algorithms = append(out.Algorithms, c.Algorithms...)
	for _, key := range c.Keys {
		if key.AccountID == id.String() {
			out.KeyIDs = append(out.KeyIDs, key.Reference)
		}
	}
	return out
}

// PublicReadEncryption maps native read metadata to the bucket owner's enrolled
// reference. Unknown identities and ambiguous responses fail closed before any
// object bytes or native encryption metadata reach the customer.
func (c EncryptionConfig) PublicReadEncryption(account string, headers http.Header) (api.ObjectEncryption, error) {
	var selection api.ObjectEncryption
	for name := range headers {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-server-side-encryption-customer-") {
			return selection, ErrUnavailable
		}
	}
	algorithms := encryptionHeaderValues(headers, "X-Amz-Server-Side-Encryption")
	keys := encryptionHeaderValues(headers, "X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id")
	buckets := encryptionHeaderValues(headers, "X-Amz-Server-Side-Encryption-Bucket-Key-Enabled")
	if len(algorithms) == 0 && len(keys) == 0 && len(buckets) == 0 {
		return selection, nil
	}
	if len(algorithms) != 1 {
		return selection, ErrUnavailable
	}
	selection.Algorithm = algorithms[0]
	if selection.Algorithm == "AES256" {
		if len(keys) != 0 || len(buckets) != 0 {
			return api.ObjectEncryption{}, ErrUnavailable
		}
		return selection, nil
	}
	if len(keys) != 1 || selection.Algorithm != "aws:kms" && selection.Algorithm != "aws:kms:dsse" {
		return api.ObjectEncryption{}, ErrUnavailable
	}
	id, err := uuid.Parse(account)
	if err != nil || id == uuid.Nil {
		return api.ObjectEncryption{}, ErrUnavailable
	}
	for _, key := range c.Keys {
		if key.AccountID == id.String() && key.ProviderKeyID == keys[0] {
			selection.KeyID = key.Reference
			break
		}
	}
	if selection.KeyID == "" || len(buckets) > 1 || selection.Algorithm == "aws:kms:dsse" && len(buckets) != 0 {
		return api.ObjectEncryption{}, ErrUnavailable
	}
	if selection.Algorithm == "aws:kms" {
		value := false
		if len(buckets) == 1 {
			if buckets[0] != "false" && buckets[0] != "true" {
				return api.ObjectEncryption{}, ErrUnavailable
			}
			value = buckets[0] == "true"
		}
		selection.BucketKeyEnabled = &value
	}
	return selection, nil
}

func encryptionHeaderValues(headers http.Header, name string) []string {
	var out []string
	for key, values := range headers {
		if strings.EqualFold(key, name) {
			out = append(out, values...)
		}
	}
	return out
}
