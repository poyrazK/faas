// apikey_app_protocol_mega3_test.go — Coverage Mega-PR #3
// cluster B: fill pkg/api coverage of the ADR-120 consumer-key
// helpers and the ADR-124 IsValidAppProtocol closed-set predicate.
// All target functions are at 0% on the baseline because the only
// existing callers are httptest-backed handler tests.
//
// Whitebox `package api`.

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// --- GenerateConsumerKey -------------------------------------------

func TestGenerateConsumerKey_HappyPathShape(t *testing.T) {
	plaintext, prefix, hash, err := GenerateConsumerKey()
	if err != nil {
		t.Fatalf("GenerateConsumerKey: %v", err)
	}
	if !strings.HasPrefix(plaintext, ConsumerKeyPrefix) {
		t.Errorf("plaintext %q missing prefix %q", plaintext, ConsumerKeyPrefix)
	}
	if got, want := len(plaintext), 3+8+1+64; got != want {
		t.Errorf("plaintext length = %d, want %d", got, want)
	}
	if len(prefix) != 8 {
		t.Errorf("prefix length = %d, want 8 hex chars", len(prefix))
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		t.Errorf("prefix %q is not valid hex: %v", prefix, err)
	}
	if len(hash) != sha256.Size {
		t.Errorf("hash length = %d, want %d (SHA-256)", len(hash), sha256.Size)
	}
}

func TestGenerateConsumerKey_RandomnessAcrossCalls(t *testing.T) {
	aPlain, _, _, err := GenerateConsumerKey()
	if err != nil {
		t.Fatalf("GenerateConsumerKey#1: %v", err)
	}
	bPlain, _, _, err := GenerateConsumerKey()
	if err != nil {
		t.Fatalf("GenerateConsumerKey#2: %v", err)
	}
	if aPlain == bPlain {
		t.Errorf("two GenerateConsumerKey calls produced identical plaintexts; rand.Reader not consulted")
	}
}

func TestGenerateConsumerKey_HashMatchesHashConsumerChain(t *testing.T) {
	plaintext, _, hash, err := GenerateConsumerKey()
	if err != nil {
		t.Fatalf("GenerateConsumerKey: %v", err)
	}
	want := HashConsumerKey(plaintext)
	if !bytes.Equal(hash, want) {
		t.Errorf("GenerateConsumerKey hash != HashConsumerKey(plaintext)")
	}
}

// --- HashConsumerKey -----------------------------------------------

func TestHashConsumerKey_DeterministicAndDistinct(t *testing.T) {
	plaintext := ConsumerKeyPrefix + "deadbeef" + "_" + strings.Repeat("ab", 32)
	a := HashConsumerKey(plaintext)
	b := HashConsumerKey(plaintext)
	if !bytes.Equal(a, b) {
		t.Error("HashConsumerKey not deterministic across calls")
	}
	other := ConsumerKeyPrefix + "cafebabe" + "_" + strings.Repeat("cd", 32)
	c := HashConsumerKey(other)
	if bytes.Equal(a, c) {
		t.Error("HashConsumerKey returned identical hashes for distinct inputs")
	}
}

// --- ValidConsumerKeyFormat ----------------------------------------

func TestValidConsumerKeyFormat_AcceptsValidShape(t *testing.T) {
	plaintext := ConsumerKeyPrefix + "deadbeef" + "_" + strings.Repeat("ab", 32)
	if !ValidConsumerKeyFormat(plaintext) {
		t.Errorf("ValidConsumerKeyFormat(%q) = false, want true", plaintext)
	}
	plaintext, _, _, err := GenerateConsumerKey()
	if err != nil {
		t.Fatalf("GenerateConsumerKey: %v", err)
	}
	if !ValidConsumerKeyFormat(plaintext) {
		t.Errorf("ValidConsumerKeyFormat(generated) = false; round-trip broken")
	}
}

func TestValidConsumerKeyFormat_RejectsMissingPrefix(t *testing.T) {
	cases := []string{
		"",
		APIKeyPrefix + strings.Repeat("a", apiKeyRandomBytes*2),
		APIKeyOIDCKeyPrefix + strings.Repeat("b", apiKeyRandomBytes*2),
		"fp_xxx_deadbeef_" + strings.Repeat("ab", 32),
	}
	for _, c := range cases {
		if ValidConsumerKeyFormat(c) {
			t.Errorf("ValidConsumerKeyFormat(%q) = true, want false (wrong prefix)", c)
		}
	}
}

func TestValidConsumerKeyFormat_RejectsMalformedBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing underscore separator", ConsumerKeyPrefix + "deadbeef" + strings.Repeat("ab", 32)},
		{"short prefix", ConsumerKeyPrefix + "abc" + "_" + strings.Repeat("ab", 32)},
		{"long prefix", ConsumerKeyPrefix + "deadbeef00" + "_" + strings.Repeat("ab", 32)},
		{"short secret", ConsumerKeyPrefix + "deadbeef" + "_" + strings.Repeat("ab", 31)},
		{"long secret", ConsumerKeyPrefix + "deadbeef" + "_" + strings.Repeat("ab", 33)},
		{"non-hex prefix", ConsumerKeyPrefix + "zzzzzzzz" + "_" + strings.Repeat("ab", 32)},
		{"non-hex secret", ConsumerKeyPrefix + "deadbeef" + "_" + strings.Repeat("zz", 32)},
		{"underscore in wrong place", ConsumerKeyPrefix + "dead_" + "beef" + "_" + strings.Repeat("ab", 32)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if ValidConsumerKeyFormat(c.body) {
				t.Errorf("ValidConsumerKeyFormat = true, want false for %q", c.body)
			}
		})
	}
}

// --- IsValidAppProtocol --------------------------------------------

func TestIsValidAppProtocol_AcceptsClosedSet(t *testing.T) {
	for _, p := range AppProtocolClosedSet {
		if !IsValidAppProtocol(p) {
			t.Errorf("IsValidAppProtocol(%q) = false, want true (in closed set)", p)
		}
	}
	for _, p := range []string{"http1", "http2", "grpc"} {
		if !IsValidAppProtocol(p) {
			t.Errorf("IsValidAppProtocol(%q) = false, want true", p)
		}
	}
}

func TestIsValidAppProtocol_RejectsUnknown(t *testing.T) {
	for _, p := range []string{
		"",
		"h2",
		"http3",
		"HTTP1",
		"GRPC",
		"http",
		"tcp",
		"unknown",
	} {
		if IsValidAppProtocol(p) {
			t.Errorf("IsValidAppProtocol(%q) = true, want false", p)
		}
	}
}

func TestAppProtocolClosedSet_NoDuplicates(t *testing.T) {
	seen := make(map[string]struct{}, len(AppProtocolClosedSet))
	for _, p := range AppProtocolClosedSet {
		if _, dup := seen[p]; dup {
			t.Errorf("AppProtocolClosedSet has duplicate %q", p)
		}
		seen[p] = struct{}{}
	}
}

// --- HashAPIKey / ValidAPIKeyFormat / ValidOIDCKeyFormat ----------

func TestHashAPIKey_DeterministicAndDistinct(t *testing.T) {
	a := HashAPIKey(APIKeyPrefix + "deadbeef")
	b := HashAPIKey(APIKeyPrefix + "deadbeef")
	if !bytes.Equal(a, b) {
		t.Error("HashAPIKey not deterministic across calls")
	}
	if len(a) != sha256.Size {
		t.Errorf("HashAPIKey length = %d, want %d", len(a), sha256.Size)
	}
	c := HashAPIKey(APIKeyPrefix + "cafebabe")
	if bytes.Equal(a, c) {
		t.Error("HashAPIKey returned identical hashes for distinct inputs")
	}
}

func TestValidAPIKeyFormat_AcceptsValidShape(t *testing.T) {
	plaintext := APIKeyPrefix + strings.Repeat("ab", apiKeyRandomBytes)
	if !ValidAPIKeyFormat(plaintext) {
		t.Errorf("ValidAPIKeyFormat(%q) = false, want true", plaintext)
	}
	plaintext, _, err := GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if !ValidAPIKeyFormat(plaintext) {
		t.Errorf("ValidAPIKeyFormat(generated) = false, want true")
	}
}

func TestValidAPIKeyFormat_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		APIKeyOIDCKeyPrefix + strings.Repeat("a", apiKeyRandomBytes*2),
		ConsumerKeyPrefix + "deadbeef_" + strings.Repeat("ab", 32),
		APIKeyPrefix + "abc",
		APIKeyPrefix + strings.Repeat("z", apiKeyRandomBytes*2),
	}
	for _, c := range cases {
		if ValidAPIKeyFormat(c) {
			t.Errorf("ValidAPIKeyFormat(%q) = true, want false", c)
		}
	}
}

func TestValidOIDCKeyFormat_AcceptsValidShape(t *testing.T) {
	plaintext := APIKeyOIDCKeyPrefix + strings.Repeat("cd", apiKeyRandomBytes)
	if !ValidOIDCKeyFormat(plaintext) {
		t.Errorf("ValidOIDCKeyFormat(%q) = false, want true", plaintext)
	}
	plaintext, _, err := GenerateOIDCKey()
	if err != nil {
		t.Fatalf("GenerateOIDCKey: %v", err)
	}
	if !ValidOIDCKeyFormat(plaintext) {
		t.Errorf("ValidOIDCKeyFormat(generated) = false, want true")
	}
}

func TestValidOIDCKeyFormat_RejectsOtherPrefixFamilies(t *testing.T) {
	cases := []string{
		"",
		APIKeyPrefix + strings.Repeat("ab", apiKeyRandomBytes),
		ConsumerKeyPrefix + "deadbeef_" + strings.Repeat("ab", 32),
	}
	for _, c := range cases {
		if ValidOIDCKeyFormat(c) {
			t.Errorf("ValidOIDCKeyFormat(%q) = true, want false", c)
		}
	}
}

// TestConstantTimeEqualHash_Mega3 covers the subtle.ConstantTimeCompare
// wrapper around ConstantTimeEqualHash (pkg/api/apikey.go:196). Renamed
// from TestConstantTimeEqualHash to avoid collision with the existing
// apikey_test.go:82 baseline test.
func TestConstantTimeEqualHash_Mega3(t *testing.T) {
	if !ConstantTimeEqualHash([]byte{}, []byte{}) {
		t.Error("empty/empty: want true")
	}
	if !ConstantTimeEqualHash([]byte{1, 2, 3}, []byte{1, 2, 3}) {
		t.Error("equal: want true")
	}
	if ConstantTimeEqualHash([]byte{1, 2, 3}, []byte{1, 2, 4}) {
		t.Error("differ in last byte: want false")
	}
	if ConstantTimeEqualHash([]byte{1, 2, 3}, []byte{1, 2}) {
		t.Error("differ in length: want false")
	}
}

// --- IsValidScope / NormalizeCreateKeyScopes ---------------------

func TestIsValidScope_AcceptsClosedSet(t *testing.T) {
	for _, s := range []string{
		ScopeAdmin, ScopeAppsRead, ScopeDeployWrite,
		ScopeSecretsRead, ScopeSecretsWrite, ScopeUsageRead,
		ScopeEnvRead, ScopeEnvWrite,
		ScopeRegistryCredentialsRead, ScopeRegistryCredentialsWrite,
		ScopeUpstreamsWrite,
		ScopeStorageManage, ScopeStorageRead, ScopeStorageWrite,
	} {
		if !IsValidScope(s) {
			t.Errorf("IsValidScope(%q) = false, want true (closed-set member)", s)
		}
	}
}

func TestIsValidScope_RejectsUnknown(t *testing.T) {
	for _, s := range []string{
		"",
		"admin ",
		"Admin",
		"deploy",
		"super:read",
	} {
		if IsValidScope(s) {
			t.Errorf("IsValidScope(%q) = true, want false", s)
		}
	}
}

func TestNormalizeCreateKeyScopes_EmptyDefaultsToAdmin(t *testing.T) {
	got, err := NormalizeCreateKeyScopes(nil)
	if err != nil {
		t.Fatalf("NormalizeCreateKeyScopes(nil): %v", err)
	}
	if len(got) != 1 || got[0] != ScopeAdmin {
		t.Errorf("nil → %v, want [%s]", got, ScopeAdmin)
	}
}

func TestNormalizeCreateKeyScopes_DedupesPreservesOrder(t *testing.T) {
	got, err := NormalizeCreateKeyScopes([]string{
		ScopeAppsRead, ScopeDeployWrite, ScopeAppsRead, ScopeAdmin,
	})
	if err != nil {
		t.Fatalf("NormalizeCreateKeyScopes: %v", err)
	}
	want := []string{ScopeAppsRead, ScopeDeployWrite, ScopeAdmin}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeCreateKeyScopes_RejectsUnknown(t *testing.T) {
	if _, err := NormalizeCreateKeyScopes([]string{ScopeAdmin, "super:read"}); err == nil {
		t.Error("unknown scope: want error, got nil")
	}
}
