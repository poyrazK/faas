package main

import (
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// devDebugActive is set once, at main-workload start, when the developer
// environment runs with a debugger listener (ADR-741). guest-init then
// reports liveness as healthy without probing the app, because a process
// paused at a breakpoint cannot answer and vmmd would otherwise destroy the
// VM in the middle of a debugging session.
var devDebugActive atomic.Bool

// StampDevDebugEnv enables the Node.js inspector for `gregale dev --debug`.
// The CLI sets api.DevDebugEnv only on its developer environment. The
// inspector listens on the guest interface so vmmd's ForwardTCPStream can
// reach it; the port is never published at the edge. Any other runtime value
// is ignored.
func StampDevDebugEnv(env []string) []string {
	if devDebugEnvValue(env, api.DevDebugEnv) != api.DevDebugRuntimeNode {
		return env
	}
	devDebugActive.Store(true)
	options := devDebugEnvValue(env, "NODE_OPTIONS")
	if strings.Contains(options, "--inspect") {
		return env
	}
	inspect := "--inspect=0.0.0.0:" + api.DevDebugNodePortString
	return devDebugSetEnv(env, "NODE_OPTIONS", strings.TrimSpace(options+" "+inspect))
}

func devDebugEnvValue(env []string, key string) string {
	prefix := key + "="
	value := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			value = strings.TrimPrefix(entry, prefix)
		}
	}
	return value
}

func devDebugSetEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}
