package objectstorage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

const encryptionTestAccount = "2fffa69f-b206-47db-8d82-8d8d394a630d"
const encryptionTestKeyID = "0f8a8987-12d6-45ad-a4bc-d384c10d9149"
const encryptionTestNativeKey = "arn:aws:kms:us-east-1:111122223333:key/abcd8987-12d6-45ad-a4bc-d384c10d9149"

func encryptionTestBackend() BackendConfig {
	b := testBackend()
	b.Encryption = EncryptionConfig{Algorithms: []string{"AES256", "aws:kms", "aws:kms:dsse"}, Keys: []EncryptionKeyBinding{{ID: encryptionTestKeyID, AccountID: encryptionTestAccount, ProviderKeyID: encryptionTestNativeKey}}}
	return b
}

func encryptionTestRegistry(t *testing.T, b BackendConfig) (*Registry, Backend) {
	t.Helper()
	r, err := NewRegistry(Config{PublicRegion: "gregale-east", DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}}, testCredentials, map[string]Factory{"s3": NewS3})
	if err != nil {
		t.Fatal(err)
	}
	placement, err := r.Default(b.Region)
	if err != nil {
		t.Fatal(err)
	}
	return r, placement
}

// adr: 554
func TestOwnedEncryptionBindingAndReconstruction(t *testing.T) {
	b := encryptionTestBackend()
	r, placement := encryptionTestRegistry(t, b)
	if b.Encryption.Keys[0].Reference != "" || placement.Fingerprint != fingerprint(testBackend()) {
		t.Fatal("configuration mutated or placement fenced")
	}
	yes := true
	requested := api.ObjectEncryption{Algorithm: "aws:kms", KeyID: placement.Encryption.Keys[0].Reference, BucketKeyEnabled: &yes}
	resolved, err := placement.Encryption.Resolve(encryptionTestAccount, requested)
	if err != nil || resolved.ProviderKeyID != encryptionTestNativeKey {
		t.Fatal(resolved, err)
	}
	yes = false
	if !*resolved.Selection.BucketKeyEnabled {
		t.Fatal("dispatch snapshot aliases request")
	}
	if _, err = placement.Encryption.Resolve(uuid.NewString(), requested); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-account key accepted", err)
	}
	raw, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	var reconstructed ResolvedObjectEncryption
	if err = json.Unmarshal(raw, &reconstructed); err != nil {
		t.Fatal(err)
	}
	_, restarted := encryptionTestRegistry(t, b)
	if err = restarted.Encryption.VerifySnapshot(encryptionTestAccount, reconstructed); err != nil {
		t.Fatal("valid reconstructed binding", err)
	}
	implicit := reconstructed
	implicit.Selection.BucketKeyEnabled = nil
	if err = restarted.Encryption.VerifySnapshot(encryptionTestAccount, implicit); !errors.Is(err, ErrConfiguration) {
		t.Fatal("reconstructed snapshot inherited a mutable bucket-key default", err)
	}
	remapped := encryptionTestBackend()
	remapped.Encryption.Keys[0].ProviderKeyID = strings.Replace(encryptionTestNativeKey, "abcd8987", "aaaa8987", 1)
	_, moved := encryptionTestRegistry(t, remapped)
	if err = moved.Encryption.VerifySnapshot(encryptionTestAccount, reconstructed); !errors.Is(err, ErrConfiguration) {
		t.Fatal("unfinished binding remapped", err)
	}
	placement.Encryption.Keys[0].ProviderKeyID = "changed"
	r.Backends()[0].Encryption.Algorithms[0] = "changed"
	current, err := r.Resolve(placement.ID, placement.Fingerprint)
	if err != nil || current.Encryption.Keys[0].ProviderKeyID != encryptionTestNativeKey || current.Encryption.Algorithms[0] != "AES256" {
		t.Fatal("registry snapshot aliases callers")
	}
	current.Encryption.Keys[0].ProviderKeyID = "changed-again"
	current, _ = r.Default(placement.Region)
	if current.Encryption.Keys[0].ProviderKeyID != encryptionTestNativeKey {
		t.Fatal("resolve snapshot aliases registry")
	}
}

func TestEncryptionConfigurationRejectsUnsafeBindings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*BackendConfig)
	}{
		{"duplicate mode", func(b *BackendConfig) { b.Encryption.Algorithms = append(b.Encryption.Algorithms, "AES256") }},
		{"unknown mode", func(b *BackendConfig) { b.Encryption.Algorithms = []string{"SSE-C"} }},
		{"unavailable KMS", func(b *BackendConfig) { b.Encryption.Algorithms = []string{"AES256"} }},
		{"wrong region", func(b *BackendConfig) {
			b.Encryption.Keys[0].ProviderKeyID = strings.Replace(encryptionTestNativeKey, "us-east-1", "us-west-2", 1)
		}},
		{"alias", func(b *BackendConfig) {
			b.Encryption.Keys[0].ProviderKeyID = strings.Replace(encryptionTestNativeKey, "key/", "alias/", 1)
		}},
		{"missing native key", func(b *BackendConfig) { b.Encryption.Keys[0].ProviderKeyID = "" }},
		{"nil owner", func(b *BackendConfig) { b.Encryption.Keys[0].AccountID = uuid.Nil.String() }},
		{"duplicate reference", func(b *BackendConfig) { b.Encryption.Keys = append(b.Encryption.Keys, b.Encryption.Keys[0]) }},
		{"shared native key", func(b *BackendConfig) {
			k := b.Encryption.Keys[0]
			k.ID = uuid.NewString()
			k.AccountID = uuid.NewString()
			b.Encryption.Keys = append(b.Encryption.Keys, k)
		}},
		{"key count overflow", func(b *BackendConfig) {
			b.Encryption.Keys = make([]EncryptionKeyBinding, api.MaxObjectEncryptionKeys+1)
		}},
		{"insecure KMS endpoint", func(b *BackendConfig) { b.Encryption.KMSEndpoint = "http://kms.example.test" }},
		{"KMS endpoint userinfo", func(b *BackendConfig) { b.Encryption.KMSEndpoint = "https://secret@kms.example.test" }},
		{"KMS endpoint query", func(b *BackendConfig) { b.Encryption.KMSEndpoint = "https://kms.example.test?secret=x" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := encryptionTestBackend()
			tc.change(&b)
			called := false
			_, err := NewRegistry(Config{DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}}, func(string) string { called = true; return "" }, map[string]Factory{"s3": NewS3})
			if err == nil || called || strings.Contains(err.Error(), encryptionTestNativeKey) {
				t.Fatal("unsafe registry accepted or disclosed key", err, called)
			}
		})
	}
}

func TestEncryptionNativeKeyCannotHaveDifferentOwnersAcrossBackends(t *testing.T) {
	b := encryptionTestBackend()
	second := encryptionTestBackend()
	second.ID = "external-b"
	second.Encryption.Keys[0].AccountID = uuid.NewString()
	_, err := NewRegistry(Config{DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b, second}}, testCredentials, map[string]Factory{"s3": NewS3})
	if err == nil || strings.Contains(err.Error(), encryptionTestNativeKey) {
		t.Fatal("ambiguous owner accepted", err)
	}
}

func TestDeclaredEncryptionRequiresImplementedProviderCapability(t *testing.T) {
	b := encryptionTestBackend()
	_, err := NewRegistry(Config{DefaultRegion: b.Region, Defaults: map[string]string{b.Region: b.ID}, Backends: []BackendConfig{b}}, testCredentials, map[string]Factory{"s3": func(BackendConfig, func(string) string) (Provider, error) { return &publicReadTestProvider{}, nil }})
	if err == nil {
		t.Fatal("unsupported encryption declared by provider")
	}
}
