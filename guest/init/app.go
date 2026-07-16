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
// values override base values for the same key.
func BuildEnv(base []string, m api.AppManifest) []string {
	merged := make(map[string]string, len(base)+len(m.Env))
	for _, kv := range base {
		if k, v, ok := cut(kv); ok {
			merged[k] = v
		}
	}
	for k, v := range m.Env {
		merged[k] = v
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
