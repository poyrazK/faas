package api

import (
	"bytes"
	"reflect"
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

// TestValidOIDCKeyFormat pins the contract that the OIDC bearer
// format is prefix-disjoint from the long-lived API key format
// (issue #270 / ADR-101). The middleware branches on the prefix,
// so a cross-prefix false-positive is a security issue. The
// negative test cases walk the same boundaries as
// TestValidAPIKeyFormat (empty, too short, non-hex, wrong prefix).
func TestValidOIDCKeyFormat(t *testing.T) {
	pt, _, _ := GenerateOIDCKey()
	tests := map[string]bool{
		pt:                        true,
		"":                        false,
		"fp_oidc_short":           false,
		"nope_" + pt:              false,
		"fp_oidc_" + "zz":         false, // wrong length + non-hex
		APIKeyOIDCKeyPrefix + "x": false,
		// Cross-prefix sanity: a long-lived fp_live_ token is
		// NOT a valid OIDC key (the prefix must be fp_oidc_).
		// Tested via GenerateAPIKey's fp_live_ prefix.
	}
	for k, want := range tests {
		if got := ValidOIDCKeyFormat(k); got != want {
			t.Errorf("ValidOIDCKeyFormat(%q) = %v, want %v", k, got, want)
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

// apikey_test.go (IAM-1, ADR-034 rev2). The fine-grained scope
// vocabulary is the load-bearing guarantee behind every requireScope
// check on the apid route table. The tests below pin
// NormalizeCreateKeyScopes — the single funnel every key-mint path
// (POST /v1/keys + the CLI device-code exchange) flows through before
// reaching the DB CHECK constraint added in migration 00044.

func TestNormalizeCreateKeyScopes(t *testing.T) {
	cases := []struct {
		name    string
		request []string
		want    []string
		wantErr bool
	}{
		{
			name:    "empty_defaults_to_admin",
			request: nil,
			want:    []string{ScopeAdmin},
		},
		{
			name:    "empty_slice_defaults_to_admin",
			request: []string{},
			want:    []string{ScopeAdmin},
		},
		{
			name:    "known_single_scope",
			request: []string{ScopeAppsRead},
			want:    []string{ScopeAppsRead},
		},
		{
			name:    "known_multi_scope_preserved",
			request: []string{ScopeAppsRead, ScopeDeployWrite},
			want:    []string{ScopeAppsRead, ScopeDeployWrite},
		},
		{
			name:    "duplicates_collapsed_first_wins",
			request: []string{ScopeAppsRead, ScopeAppsRead, ScopeDeployWrite},
			want:    []string{ScopeAppsRead, ScopeDeployWrite},
		},
		{
			name: "all_scopes_accepted",
			request: []string{
				ScopeAdmin, ScopeAppsRead, ScopeDeployWrite,
				ScopeSecretsRead, ScopeSecretsWrite, ScopeUsageRead,
				ScopeEnvRead, ScopeEnvWrite, ScopeRegistryCredentialsRead,
				ScopeRegistryCredentialsWrite, ScopeUpstreamsWrite,
				ScopeStorageManage, ScopeStorageRead, ScopeStorageWrite,
			},
			want: []string{
				ScopeAdmin, ScopeAppsRead, ScopeDeployWrite,
				ScopeSecretsRead, ScopeSecretsWrite, ScopeUsageRead,
				ScopeEnvRead, ScopeEnvWrite, ScopeRegistryCredentialsRead,
				ScopeRegistryCredentialsWrite, ScopeUpstreamsWrite,
				ScopeStorageManage, ScopeStorageRead, ScopeStorageWrite,
			},
		},
		{
			name:    "unknown_rejected",
			request: []string{ScopeAdmin, "banana"},
			wantErr: true,
		},
		{
			name: "legacy_coarse_vocabulary_now_rejected",
			// Rev1 vocabulary (read|write) is gone — a customer
			// SDK that still posts these must fail at the
			// handler so the error surfaces in their tooling.
			request: []string{"read"},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeCreateKeyScopes(tc.request)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result=%v)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestIsValidScope pins the closed vocabulary at the package surface —
// mirror of the DB CHECK constraint added in migration 00046
// (widened by 00061 to admit env:read/env:write). If a route file
// references a scope that isn't in this set, it would fail closed
// everywhere: the migration prevents the INSERT,
// NormalizeCreateKeyScopes blocks the mint, and principalHasScope
// never sees it from a valid key.
func TestIsValidScope(t *testing.T) {
	valid := []string{
		ScopeAdmin, ScopeAppsRead, ScopeDeployWrite,
		ScopeSecretsRead, ScopeSecretsWrite, ScopeUsageRead,
		// Issue #395 / ADR-045: env surfaces.
		ScopeEnvRead, ScopeEnvWrite,
		ScopeRegistryCredentialsRead, ScopeRegistryCredentialsWrite,
		ScopeUpstreamsWrite,
		ScopeStorageManage, ScopeStorageRead, ScopeStorageWrite,
	}
	for _, s := range valid {
		if !IsValidScope(s) {
			t.Errorf("expected %q to be a valid scope", s)
		}
	}
	invalid := []string{"", "read", "write", "ROOT", "superuser", "deploy", "admin:write"}
	for _, s := range invalid {
		if IsValidScope(s) {
			t.Errorf("expected %q to be rejected", s)
		}
	}
}

// TestScopeSurfaceConstants pin the named surface-set shapes used by
// requireScope callers. Misspelled constant names are compile errors;
// this test pins the actual membership so a future "rename
// ScopesReadSurface to ScopesReadScope" lands here too.
func TestScopeSurfaceConstants(t *testing.T) {
	mustContain := func(name string, set []string, want ...string) {
		t.Helper()
		contains := map[string]bool{}
		for _, s := range set {
			contains[s] = true
		}
		for _, w := range want {
			if !contains[w] {
				t.Errorf("%s missing %q (have %v)", name, w, set)
			}
		}
	}
	mustContain("ScopesAdminOnly", ScopesAdminOnly, ScopeAdmin)
	mustContain("ScopesReadSurface", ScopesReadSurface, ScopeAdmin, ScopeAppsRead)
	mustContain("ScopesDeploymentReadSurface", ScopesDeploymentReadSurface, ScopeAdmin, ScopeAppsRead, ScopeDeployWrite)
	mustContain("ScopesUsageReadSurface", ScopesUsageReadSurface, ScopeAdmin, ScopeUsageRead)
	mustContain("ScopesSecretsWriteSurface", ScopesSecretsWriteSurface, ScopeAdmin, ScopeSecretsWrite)
	mustContain("ScopesDeployWriteSurface", ScopesDeployWriteSurface, ScopeAdmin, ScopeDeployWrite)
	// Issue #395 / ADR-045: env:write mirrors secrets:write in shape.
	mustContain("ScopesEnvWriteSurface", ScopesEnvWriteSurface, ScopeAdmin, ScopeEnvWrite)
	mustContain("ScopesStorageManageSurface", ScopesStorageManageSurface, ScopeAdmin, ScopeStorageManage)
	mustContain("ScopesStorageReadSurface", ScopesStorageReadSurface, ScopeAdmin, ScopeStorageRead)
	mustContain("ScopesStorageWriteSurface", ScopesStorageWriteSurface, ScopeAdmin, ScopeStorageWrite)
	mustContain("ScopesStorageListSurface", ScopesStorageListSurface, ScopeAdmin, ScopeStorageManage, ScopeStorageRead, ScopeStorageWrite)
}
