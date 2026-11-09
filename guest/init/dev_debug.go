package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// devDebugActive is set once, at main-workload start, when guest-init started
// the developer environment's debugger listener (ADR-741). While a debugger
// is attached to it, guest-init reports liveness as healthy without probing
// the app: a process paused at a breakpoint cannot answer, and vmmd would
// otherwise destroy the VM in the middle of a debugging session.
var devDebugActive atomic.Bool

// devDebugPreloadPath holds the inspector preload. /run/guest-init is inside
// the pivoted root, so the workload can read it.
const devDebugPreloadPath = "/run/guest-init/dev-debug.cjs"

// devDebugPreload opens the inspector in the first Node process that runs the
// app's own code. Package managers and development-server supervisors are Node
// processes too: with --inspect in NODE_OPTIONS `npm start` took the port and
// the app's inspector failed with "address already in use", so the debugger
// attached to npm. A watcher (next dev, nodemon, tsx watch, ts-node-dev,
// node --watch) restarts a child that runs the app, so the child must get the
// port, not the watcher that never runs a route handler.
const devDebugPreload = `'use strict';
(function () {
  const script = String(process.argv[1] || '');
  const supervisors = 'npm|yarn|pnpm|corepack|nodemon|tsx|ts-node-dev|concurrently|npm-run-all|npm-run-all2';
  if (new RegExp('[\\\\/]node_modules[\\\\/](' + supervisors + ')[\\\\/]').test(script) ||
      /[\\/]node_modules[\\/]next[\\/]dist[\\/]bin[\\/]next(\.js)?$/.test(script) ||
      /[\\/]node_modules[\\/]\.bin[\\/](next|nodemon|tsx|ts-node-dev|concurrently|npm-run-all|run-p|run-s)$/.test(script) ||
      /[\\/](npm|npx|yarn|yarnpkg|pnpm|pnpx|corepack)(-cli)?(\.c?js)?$/.test(script) ||
      /[\\/]yarn-[0-9][^\\/]*\.c?js$/.test(script) ||
      process.execArgv.some(function (arg) { return arg === '--watch' || arg.indexOf('--watch-path') === 0; })) {
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
// The CLI sets api.DevDebugEnv on its developer environment; developer is the
// schedd-owned platform marker (api.PlatformDeveloperEnvironmentEnv), so a
// value set on any other app, through env, secrets or the manifest, is
// ignored. The inspector listens on the guest interface so vmmd's
// ForwardTCPStream can reach it; the port is never published at the edge.
// Any other runtime value is ignored. preload is the path of devDebugPreload;
// empty falls back to a plain --inspect flag.
//
// devDebugActive is set only when the tunnel can reach the inspector: when
// this function adds it, or when the app's own NODE_OPTIONS already listen on
// 0.0.0.0:9229. An app inspector elsewhere is left alone and keeps ordinary
// liveness.
func StampDevDebugEnv(env []string, preload string, developer bool) []string {
	if devDebugEnvValue(env, api.DevDebugEnv) != api.DevDebugRuntimeNode || !developer {
		return env
	}
	options := devDebugEnvValue(env, "NODE_OPTIONS")
	if strings.Contains(options, "--inspect") {
		devDebugActive.Store(devDebugInspectReachable(options))
		return env
	}
	devDebugActive.Store(true)
	flag := "--inspect=0.0.0.0:" + api.DevDebugNodePortString
	if preload != "" {
		flag = "--require=" + preload
	}
	return devDebugSetEnv(env, "NODE_OPTIONS", strings.TrimSpace(options+" "+flag))
}

// devDebugInspectReachable reports whether an app-supplied --inspect flag
// listens where the debugger tunnel connects: all interfaces, port 9229.
func devDebugInspectReachable(options string) bool {
	for _, field := range strings.Fields(options) {
		name, value, _ := strings.Cut(field, "=")
		switch name {
		case "--inspect", "--inspect-brk", "--inspect-wait":
		default:
			continue
		}
		switch value {
		case "0.0.0.0:" + api.DevDebugNodePortString, "[::]:" + api.DevDebugNodePortString:
			return true
		}
	}
	return false
}

// writeDevDebugPreload writes devDebugPreload under dir and returns its path.
func writeDevDebugPreload(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(devDebugPreload), 0o644)
}

// devDebugTCPTables are the kernel socket tables devDebugAttached reads.
var devDebugTCPTables = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// devDebugAttached reports whether a debugger connection to the inspector
// port is established. Only then may a breakpoint have paused the app; with
// no debugger attached a hung app still fails liveness and is replaced.
func devDebugAttached() bool {
	for _, path := range devDebugTCPTables {
		if devDebugTableHasEstablished(path, api.DevDebugNodePort) {
			return true
		}
	}
	return false
}

// devDebugTableHasEstablished scans one /proc/net/tcp{,6} table for an
// ESTABLISHED (state 01) socket whose local port is port.
func devDebugTableHasEstablished(path string, port int) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[3] != "01" {
			continue
		}
		_, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		local, err := strconv.ParseUint(portHex, 16, 16)
		if err == nil && int(local) == port {
			return true
		}
	}
	return false
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
