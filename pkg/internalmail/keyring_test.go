package internalmail

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func encryptionIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func keyringForTest(t *testing.T, legacy *age.X25519Identity, identities ...*age.X25519Identity) *KeyringCodec {
	t.Helper()
	codec, err := NewKeyringCodec("platform", EncryptionKeyID(legacy), identities)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func TestKeyringLegacyAndVersionedEncryptionAndReplay(t *testing.T) {
	legacy, next := encryptionIdentity(t), encryptionIdentity(t)
	codec := keyringForTest(t, legacy, legacy, next)
	resource, plain := "message:one", []byte("secret short verification code 123456")
	old := AgeCodec{Identity: legacy, Namespace: "platform"}
	sealedV1, err := old.Seal(resource, plain)
	if err != nil {
		t.Fatal(err)
	}
	fpV1, err := old.Fingerprint(resource, plain)
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.selectKey(EncryptionKeyID(next), 1); err != nil {
		t.Fatal(err)
	}
	sealedV2, err := codec.Seal(resource, plain)
	if err != nil || !bytes.HasPrefix(sealedV2, []byte(encryptionPrefix+EncryptionKeyID(next)+":")) || bytes.Contains(sealedV2, plain) {
		t.Fatal("invalid versioned ciphertext", err)
	}
	fpV2, err := codec.Fingerprint(resource, plain)
	if err != nil || len(fpV2) != 103 || bytes.Contains(fpV2, plain) {
		t.Fatal("invalid versioned fingerprint", err)
	}
	restarted := keyringForTest(t, legacy, legacy, next)
	for _, sealed := range [][]byte{sealedV1, sealedV2} {
		opened, err := restarted.Open(resource, sealed)
		if err != nil || !bytes.Equal(opened, plain) {
			t.Fatal("rotation or restart lost decryption", err)
		}
		if _, err := restarted.Open("message:other", sealed); err == nil {
			t.Fatal("ciphertext moved between resources")
		}
	}
	for _, fp := range [][]byte{fpV1, fpV2} {
		if err := restarted.VerifyFingerprint(resource, plain, fp); err != nil {
			t.Fatal("rotation changed saved replay verification", err)
		}
		for _, tc := range []struct {
			resource string
			plain    []byte
		}{{"message:other", plain}, {resource, []byte("different body")}} {
			if err := restarted.VerifyFingerprint(tc.resource, tc.plain, fp); !errors.Is(err, ErrConflict) {
				t.Fatal("changed replay accepted", err)
			}
		}
	}
	otherNamespace, err := NewKeyringCodec("elsewhere", EncryptionKeyID(legacy), []*age.X25519Identity{legacy, next})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherNamespace.Open(resource, sealedV2); err == nil || otherNamespace.VerifyFingerprint(resource, plain, fpV2) == nil {
		t.Fatal("namespace binding lost")
	}
	tampered := append([]byte(nil), sealedV2...)
	copy(tampered[len(encryptionPrefix):], EncryptionKeyID(legacy))
	if _, err := codec.Open(resource, tampered); err == nil {
		t.Fatal("ciphertext key ID was not authenticated")
	}
	tamperedFP := append([]byte(nil), fpV2...)
	tamperedFP[len(tamperedFP)-1] ^= 1
	if codec.VerifyFingerprint(resource, plain, tamperedFP) == nil || codec.VerifyFingerprint(resource, plain, fpV2[:len(fpV2)-1]) == nil {
		t.Fatal("tampered or truncated HMAC accepted")
	}
	missing := keyringForTest(t, legacy, next)
	if _, err := missing.Open(resource, sealedV1); !errors.Is(err, ErrEncryptionKeyUnavailable) {
		t.Fatal("missing legacy encryption key substituted", err)
	}
	if missing.VerifyFingerprint(resource, plain, fpV1) == nil {
		t.Fatal("missing legacy verifier substituted")
	}
	missingNext := keyringForTest(t, legacy, legacy)
	if _, err := missingNext.Open(resource, sealedV2); !errors.Is(err, ErrEncryptionKeyUnavailable) {
		t.Fatal("missing versioned encryption key substituted", err)
	}
	if err := missingNext.VerifyFingerprint(resource, plain, fpV2); !errors.Is(err, ErrEncryptionKeyUnavailable) {
		t.Fatal("missing versioned HMAC key substituted", err)
	}
}

func TestKeyringDelayedRefreshCannotUndoNewerSelection(t *testing.T) {
	legacy, next := encryptionIdentity(t), encryptionIdentity(t)
	codec := keyringForTest(t, legacy, legacy, next)
	if err := codec.selectKey(EncryptionKeyID(next), 2); err != nil {
		t.Fatal(err)
	}
	// A status read completed before the newer activation may finish its
	// local refresh afterwards. It cannot restore either a prior key or v1.
	for _, revision := range []int64{0, 1} {
		if err := codec.selectKey(EncryptionKeyID(legacy), revision); err != nil {
			t.Fatal(err)
		}
		if id, versioned := codec.selection(); id != EncryptionKeyID(next) || !versioned {
			t.Fatal("late refresh restored an older writer or envelope format")
		}
	}
	if err := codec.selectKey(EncryptionKeyID(legacy), 2); !errors.Is(err, ErrEncryptionRevision) {
		t.Fatal("conflicting selections shared one revision", err)
	}
	if err := codec.selectKey(EncryptionKeyID(legacy), 3); err != nil {
		t.Fatal("a newer deliberate activation was rejected", err)
	}
}

func TestKeyringPrivateConfigurationAndKeyIdentityValidation(t *testing.T) {
	dir := t.TempDir()
	legacy, next := encryptionIdentity(t), encryptionIdentity(t)
	legacyPath, nextPath := filepath.Join(dir, "legacy.age"), filepath.Join(dir, "next.age")
	for path, identity := range map[string]*age.X25519Identity{legacyPath: legacy, nextPath: next} {
		if err := os.WriteFile(path, []byte(identity.String()), 0400); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "keyring.json")
	write := func(input any) {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0400); err != nil {
			t.Fatal(err)
		}
	}
	cfg := Config{Namespace: "platform", IdentityPath: legacyPath, KeyringPath: path}
	write(keyringConfig{IdentityPaths: []string{nextPath}})
	codec, err := LoadMailCodec(cfg)
	if err != nil || len(codec.keys) != 2 || codec.legacyID != EncryptionKeyID(legacy) {
		t.Fatal("could not stage a read-only identity", err)
	}
	// After a permitted retirement the legacy ID remains pinned, while its
	// private file can be omitted. No fallback silently loads another key.
	write(keyringConfig{LegacyKeyID: EncryptionKeyID(legacy), IdentityPaths: []string{nextPath}})
	cfg.IdentityPath = "/missing/legacy.age"
	if codec, err := LoadMailCodec(cfg); err != nil || codec.keys[EncryptionKeyID(legacy)] != nil {
		t.Fatal("explicit legacy ID required an obsolete private file", err)
	}
	for _, input := range []any{
		keyringConfig{LegacyKeyID: "invalid", IdentityPaths: []string{nextPath}},
		keyringConfig{LegacyKeyID: EncryptionKeyID(legacy), IdentityPaths: []string{nextPath, nextPath}},
		keyringConfig{LegacyKeyID: EncryptionKeyID(legacy), IdentityPaths: []string{"relative.age"}},
		map[string]any{"identity_paths": []string{nextPath}, "private_key": next.String()},
	} {
		write(input)
		if _, err := LoadMailCodec(cfg); err == nil || strings.Contains(err.Error(), next.String()) {
			t.Fatal("unsafe private keyring accepted or key exposed")
		}
	}
	write(keyringConfig{IdentityPaths: []string{nextPath}})
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMailCodec(cfg); err == nil {
		t.Fatal("loose keyring permissions accepted")
	}
	if _, err := NewKeyringCodec("../bad", EncryptionKeyID(legacy), []*age.X25519Identity{legacy}); err == nil {
		t.Fatal("invalid namespace accepted")
	}
}
