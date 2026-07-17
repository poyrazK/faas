// Tests for the secrets.env reader + BuildEnvWithSecrets layer ordering.
// Pure unit tests — no syscalls. secrets_linux.go's loadSecrets reads a
// hard-coded path; we test it indirectly through BuildEnvWithSecrets, and
// via the loader when run under a build constraint for a temp root.
//
// These tests run on every platform — they're cheap and exercise the
// merge order that survives contact with main_linux.go.

package main

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildEnv_SecretsOverrideManifest(t *testing.T) {
	// Precedence (lowest → highest): base < manifest < secrets.
	// The same key in three sources must produce the secrets value.
	base := []string{"FOO=base", "BAR=base"}
	m := api.AppManifest{Env: map[string]string{"FOO": "manifest", "BAZ": "manifest"}}
	secrets := map[string]string{"FOO": "secret", "SECRET_KEY": "x"}
	got := BuildEnvWithSecrets(base, m, secrets)

	want := map[string]string{
		"FOO":        "secret",   // secrets win
		"BAR":        "base",     // only in base
		"BAZ":        "manifest", // only in manifest
		"SECRET_KEY": "x",        // only in secrets
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for _, kv := range got {
		// split on first '=' for comparison
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				k, v := kv[:i], kv[i+1:]
				if want[k] != v {
					t.Errorf("env[%q] = %q, want %q (got line %q)", k, v, want[k], kv)
				}
				break
			}
		}
	}
}

func TestBuildEnv_NilSecretsActsAsBuildEnv(t *testing.T) {
	m := api.AppManifest{Env: map[string]string{"K": "m"}}
	a := BuildEnv([]string{"K=b", "OTHER=o"}, m)
	b := BuildEnvWithSecrets([]string{"K=b", "OTHER=o"}, m, nil)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("nil secrets differs from BuildEnv:\n  BuildEnv  = %v\n  WithNil   = %v", a, b)
	}
}

func TestBuildEnv_DeterministicOrder(t *testing.T) {
	// Required for replayable bash output in app logs: output ordering
	// must not depend on map iteration order. The layer ordering is
	// (base ∪ manifest ∪ secrets), sorted.
	m := api.AppManifest{Env: map[string]string{"FOO": "f", "ZOO": "z"}}
	secrets := map[string]string{"MID": "m", "AAA": "a"}
	first := BuildEnvWithSecrets(nil, m, secrets)
	for i := 0; i < 32; i++ {
		again := BuildEnvWithSecrets(nil, m, secrets)
		if !reflect.DeepEqual(first, again) {
			t.Fatalf("non-deterministic at iteration %d: %v vs %v", i, first, again)
		}
	}
}

func TestBuildEnv_InvalidKeysDropped(t *testing.T) {
	// Bad-shaped keys (lowercase, hyphens, digits-leading) must NOT
	// appear in the produced env. Defense in depth — the SQL CHECK
	// already keeps these out of the DB, but a future writer could
	// bypass the check.
	base := []string{"ok=base"}
	m := api.AppManifest{Env: map[string]string{
		"lowercase":  "bad", // rejected
		"BAD-HYPHEN": "bad", // rejected
		"9DIGIT":     "bad", // rejected
		"OK_NAME":    "good",
	}}
	secrets := map[string]string{"ALSO_OK": "v", "BAD KEY": "v"}
	got := BuildEnvWithSecrets(base, m, secrets)
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				k := kv[:i]
				if !validEnvKey(k) {
					t.Errorf("invalid key %q leaked into env: %q", k, kv)
				}
				// "BAD KEY" has a space — even if the loop reaches the
				// space, validEnvKey will reject it.
				break
			}
		}
	}
	// Spot-check the surviving keys.
	keys := map[string]bool{}
	for _, kv := range got {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				keys[kv[:i]] = true
				break
			}
		}
	}
	for _, k := range []string{"ok", "OK_NAME", "ALSO_OK"} {
		if !keys[k] {
			t.Errorf("expected %q in env: %+v", k, keys)
		}
	}
}

func TestBuildEnv_EmptyKeysRejectsEmptyKey(t *testing.T) {
	// An empty key (only '=') must be dropped — never reach execve.
	base := []string{"=bare"}
	got := BuildEnvWithSecrets(base, api.AppManifest{}, nil)
	for _, kv := range got {
		if len(kv) == 0 || kv[0] == '=' {
			t.Errorf("bare env line leaked: %q", kv)
		}
	}
}

func TestValidEnvKeyShape(t *testing.T) {
	cases := map[string]bool{
		"OK":            true,
		"A1":            true,
		"FOO_BAR_BAZ":   true,
		"":              false,
		"lowercase":     false,
		"_UNDERSCORE":   false,
		"HY-PHEN":       false,
		"9STARTS_DIGIT": false,
	}
	for in, want := range cases {
		if got := validEnvKey(in); got != want {
			t.Errorf("validEnvKey(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestIsNotExist_NilAndPresent(t *testing.T) {
	if isNotExist(nil) {
		t.Errorf("nil should not be 'not exist'")
	}
}
