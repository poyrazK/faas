package api

import (
	"bytes"
	"testing"
)

func TestGenerateAPIKey(t *testing.T) {
	pt, hash, err := GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidAPIKeyFormat(pt) {
		t.Errorf("generated key %q has invalid format", pt)
	}
	if !bytes.Equal(hash, HashAPIKey(pt)) {
		t.Error("returned hash does not match HashAPIKey(plaintext)")
	}
	if len(hash) != 32 {
		t.Errorf("hash len = %d, want 32 (sha256)", len(hash))
	}
}

func TestKeysAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		pt, _, err := GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if seen[pt] {
			t.Fatalf("duplicate key generated: %s", pt)
		}
		seen[pt] = true
	}
}

func TestValidAPIKeyFormat(t *testing.T) {
	pt, _, _ := GenerateAPIKey()
	tests := map[string]bool{
		pt:                   true,
		"":                   false,
		"fp_live_short":      false,
		"nope_" + pt:         false,
		"fp_live_" + "zz":    false, // wrong length + non-hex
		APIKeyPrefix + "xyz": false,
	}
	for k, want := range tests {
		if got := ValidAPIKeyFormat(k); got != want {
			t.Errorf("ValidAPIKeyFormat(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestConstantTimeEqualHash(t *testing.T) {
	pt, hash, _ := GenerateAPIKey()
	if !ConstantTimeEqualHash(hash, HashAPIKey(pt)) {
		t.Error("matching hashes should compare equal")
	}
	if ConstantTimeEqualHash(hash, HashAPIKey("fp_live_different")) {
		t.Error("different hashes should not compare equal")
	}
}
