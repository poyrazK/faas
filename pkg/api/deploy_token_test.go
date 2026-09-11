package api

import (
	"strings"
	"testing"
)

func TestGenerateDeployToken(t *testing.T) {
	plain, hash, err := GenerateDeployToken()
	if err != nil {
		t.Fatalf("GenerateDeployToken: %v", err)
	}
	if !ValidDeployTokenFormat(plain) {
		t.Fatalf("generated token has invalid format: %q", plain)
	}
	if len(hash) != 32 {
		t.Fatalf("hash length = %d, want 32", len(hash))
	}
	if got := HashAPIKey(plain); string(got) != string(hash) {
		t.Fatal("generated hash does not match HashAPIKey")
	}
	if strings.HasPrefix(plain, APIKeyPrefix) || strings.HasPrefix(plain, APIKeyOIDCKeyPrefix) {
		t.Fatal("deploy token prefix overlaps another bearer prefix")
	}
}

func TestValidDeployTokenFormat(t *testing.T) {
	valid := DeployTokenPrefix + strings.Repeat("a", apiKeyRandomBytes*2)
	for _, tc := range []struct {
		name  string
		token string
		want  bool
	}{
		{"valid", valid, true},
		{"wrong prefix", APIKeyPrefix + strings.Repeat("a", 48), false},
		{"short", DeployTokenPrefix + "abc", false},
		{"non hex", DeployTokenPrefix + strings.Repeat("g", 48), false},
		{"suffix", valid + "x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidDeployTokenFormat(tc.token); got != tc.want {
				t.Fatalf("ValidDeployTokenFormat(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}
