// Command init is guest-init: PID 1 inside every microVM (spec §4.8). It is a
// tiny static binary injected by imaged as /sbin/init. Boot path: mount
// proc/sys/tmp, assemble the two-drive overlay, bring up eth0 (always
// 10.0.0.2/30, ADR-009), apply the app.json env, exec the app as the app user,
// and supervise it (restart ≤3, then exit the VM). The resume path (post-restore)
// re-seeds entropy and steps the clock before readiness.
//
// This file holds the platform-independent logic so it is unit-testable; the
// Linux mount/network/exec syscalls live in boot_linux.go.
package main

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// MaxRestarts is the supervisor's crash-loop budget (spec §4.8: restart ≤3, then
// the VM exits and schedd marks the instance FAILED).
const MaxRestarts = 3

// BuildEnv merges the manifest env over a base environment and returns a
// deterministic, deduplicated "KEY=VALUE" slice suitable for execve. Manifest
// values override base values for the same key. The optional secrets layer
// (loaded from /etc/faas/secrets.env by secrets.go) is applied LAST so
// customers' explicit credential values win over any default in the manifest.
func BuildEnv(base []string, m api.AppManifest) []string {
	return BuildEnvWithSecrets(base, m, nil)
}

// BuildEnvWithSecrets is the secrets-aware variant. Pass nil secrets to get
// the same behavior as BuildEnv. Precedence (lowest to highest):
//
//	base (os.Environ) < manifest env < secrets env
//
// All three sources must conform to the [A-Z][A-Z0-9_]* key shape; entries
// that do not are silently skipped (defense in depth — the SQL CHECK
// already enforces shape, but an out-of-band writer shouldn't be able to
// crash execve with a malformed env entry).
func BuildEnvWithSecrets(base []string, m api.AppManifest, secrets map[string]string) []string {
	merged := make(map[string]string, len(base)+len(m.Env)+len(secrets))
	for _, kv := range base {
		if k, v, ok := cut(kv); ok && validEnvKey(k) {
			merged[k] = v
		}
	}
	for k, v := range m.Env {
		if validEnvKey(k) {
			merged[k] = v
		}
	}
	for k, v := range secrets {
		if validEnvKey(k) {
			merged[k] = v
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+merged[k])
	}
	return out
}

// validEnvKey enforces the same ^[A-Z][A-Z0-9_]* shape the SQL CHECK and
// apid validator do. Untyped key names reaching execve can take several
// unfun paths through libc; we'd rather drop a single bad entry than risk
// it leaking into the spawning environ.
func validEnvKey(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	c := k[0]
	if c < 'A' || c > 'Z' {
		return false
	}
	for i := 1; i < len(k); i++ {
		c := k[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return true
}

// cut splits "KEY=VALUE" once. Entries without '=' are treated as KEY="".
func cut(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	if kv == "" {
		return "", "", false
	}
	return kv, "", true
}
