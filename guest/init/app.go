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
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// MaxRestarts is the legacy/default supervisor crash-loop budget. New
// manifests may override it with AppManifest.MaxRetries; zero means inherit
// this compatibility default because the guest does not carry plan context.
const MaxRestarts = 3

// supervisorPolicyFromManifest resolves the guest-side portion of the
// lifecycle contract. The API validates the closed-set policy and plan caps
// before the manifest reaches the guest; this helper only supplies the
// backwards-compatible default for old layers and zero-valued fields.
func supervisorPolicyFromManifest(m api.AppManifest) (string, int) {
	max := m.MaxRetries
	if max <= 0 {
		max = MaxRestarts
	}
	return m.EffectiveRestartPolicy(), max
}

// BuildEnv merges the manifest env over a base environment and returns a
// deterministic, deduplicated "KEY=VALUE" slice suitable for execve. Manifest
// values override base values for the same key. The optional secrets layer
// (loaded from /etc/faas/secrets.env by secrets.go) is applied LAST so
// customers' explicit credential values win over any default in the manifest.
func BuildEnv(base []string, m api.AppManifest) []string {
	return BuildEnvWithSecrets(base, m, nil, nil)
}

// BuildEnvWithSecrets is the secrets-aware variant. Pass nil secrets and
// nil apiEnv to get the same behavior as BuildEnv. Precedence (lowest to
// highest):
//
//	base (os.Environ) < manifest env < apiEnv < secrets env
//
// All four sources must conform to the [A-Z][A-Z0-9_]* key shape; entries
// that do not are silently skipped (defense in depth — the SQL CHECK
// already enforces shape, but an out-of-band writer shouldn't be able to
// crash execve with a malformed env entry).
//
// The apiEnv layer is issue #395 / ADR-045 — the plaintext per-app env
// store (LOG_LEVEL, FEATURE_X, …). Non-sensitive runtime config sits here;
// credentials stay in the secrets layer. The ordering "secrets > apiEnv >
// manifest" matches the issue's plaintext rationale: a runtime tweak via
// PUT /v1/apps/{slug}/env/{key} overrides the image's default env but
// cannot accidentally clobber a credential set via the secret surface.
func BuildEnvWithSecrets(base []string, m api.AppManifest, secrets, apiEnv map[string]string) []string {
	merged := make(map[string]string, len(base)+len(m.Env)+len(secrets)+len(apiEnv))
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
	// apiEnv layer (issue #395 / ADR-045): applied AFTER manifest so a
	// runtime PUT overrides the image's default env. Plaintext by
	// contract — see the file header in cmd/apid/handlers_env.go for
	// the trust-model rationale.
	for k, v := range apiEnv {
		if validEnvKey(k) {
			merged[k] = v
		}
	}
	// Secrets layer stays LAST: a customer credential always wins
	// over any default, manifest env, or plaintext api_env row.
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

// StampOverridePortEnv appends "PORT=<port>" to env and returns the
// augmented slice. It is the pure helper behind issue #460 / ADR-053
// (PR-C) — guest-init's runAppWithEnv appends to BuildEnvWithSecrets'
// output so customer-set PORT entries (whether in manifest env, the
// apiEnv layer, or sealed secrets) cannot accidentally shadow the
// platform contract. Exported for tests; the test asserts the
// precedence directly without launching the customer's process.
func StampOverridePortEnv(env []string, port int) []string {
	return append(env, "PORT="+strconv.Itoa(port))
}

// StampTraceparentEnv appends TRACEPARENT=<tp> to env when tp is
// non-empty (issue #555 PR-4). W3C trace context propagates from the
// gateway through schedd → vmmd → guest-init via the vsock resume hook
// JSON body; the guest stamps it onto the runner env so the customer's
// handler can pick it up via `process.env.TRACEPARENT` and emit its own
// child spans. Empty traceparent is a no-op (legacy single-box without
// OTel). The customer can override TRACEPARENT in their manifest env,
// though that breaks the per-request correlation chain — the platform
// contract is "use the platform-supplied TRACEPARENT". Exported for
// tests; the precedence assertion lives in env_linux_test.go.
func StampTraceparentEnv(env []string, tp string) []string {
	if tp == "" {
		return env
	}
	return append(env, "TRACEPARENT="+tp)
}
