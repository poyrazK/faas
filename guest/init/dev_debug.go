package main

import (
	"os"
	"path/filepath"
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

// devDebugPreloadPath holds the inspector preload. /run/guest-init is inside
// the pivoted root, so the workload can read it.
const devDebugPreloadPath = "/run/guest-init/dev-debug.cjs"

// devDebugPreload opens the inspector in the first Node process that is not a
// package manager. `npm start` is itself a Node process: with --inspect in
// NODE_OPTIONS npm took the port and the app's own inspector failed with
// "address already in use", so the debugger attached to npm.
const devDebugPreload = `'use strict';
(function () {
  const script = String(process.argv[1] || '');
  if (/[\\/]node_modules[\\/](npm|yarn|pnpm|corepack)[\\/]/.test(script) ||
      /[\\/](npm|npx|yarn|yarnpkg|pnpm|pnpx|corepack)(-cli)?(\.c?js)?$/.test(script) ||
      /[\\/]yarn-[0-9][^\\/]*\.c?js$/.test(script)) {
    return;
  }
  try {
    require('inspector').open(` + api.DevDebugNodePortString + `, '0.0.0.0');
  } catch (_) {
    // A forked child of the app finds the port taken; the parent keeps it.
  }
})();
`

// StampDevDebugEnv enables the Node.js inspector for `gregale dev --debug`.
// The CLI sets api.DevDebugEnv only on its developer environment. The
// inspector listens on the guest interface so vmmd's ForwardTCPStream can
// reach it; the port is never published at the edge. Any other runtime value
// is ignored. preload is the path of devDebugPreload; empty falls back to a
// plain --inspect flag.
func StampDevDebugEnv(env []string, preload string) []string {
	if devDebugEnvValue(env, api.DevDebugEnv) != api.DevDebugRuntimeNode {
		return env
	}
	devDebugActive.Store(true)
	options := devDebugEnvValue(env, "NODE_OPTIONS")
	if strings.Contains(options, "--inspect") {
		return env
	}
	flag := "--inspect=0.0.0.0:" + api.DevDebugNodePortString
	if preload != "" {
		flag = "--require=" + preload
	}
	return devDebugSetEnv(env, "NODE_OPTIONS", strings.TrimSpace(options+" "+flag))
}

// writeDevDebugPreload writes devDebugPreload under dir and returns its path.
func writeDevDebugPreload(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(devDebugPreload), 0o644)
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
