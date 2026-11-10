package main

// adr: 20261109140000 — after each reseed barrier guest-init reports running
// interpreter processes the barrier did not reach; it never refuses a restore.

import (
	"reflect"
	"testing"
)

func TestReseedRuntimeForExe(t *testing.T) {
	for exe, want := range map[string]string{
		"/usr/local/bin/node": "node", "/usr/bin/nodejs": "node", "/usr/local/bin/node (deleted)": "node",
		"/usr/bin/python3": "python", "/usr/local/bin/python3.12": "python", "/opt/pypy3.10": "python",
		"/root/.bun/bin/bun": "bun", "/usr/bin/deno": "deno",
		"/usr/bin/java": "", "/app/server": "", "/usr/bin/python3-config": "", "/bin/sh": "",
	} {
		if got := reseedRuntimeForExe(exe); got != want {
			t.Errorf("reseedRuntimeForExe(%q) = %q, want %q", exe, got, want)
		}
	}
}

func TestUnregisteredRuntimes(t *testing.T) {
	candidates := []reseedCandidate{
		{pid: 10, exe: "/usr/local/bin/node", entry: "/app/server.js"},                                 // registered
		{pid: 11, exe: "/usr/local/bin/node", entry: "/usr/local/lib/node_modules/npm/bin/npm-cli.js"}, // launcher
		{pid: 12, exe: "/usr/local/bin/node", entry: "/app/worker.js"},                                 // missed
		{pid: 13, exe: "/root/.bun/bin/bun", entry: "/app/index.ts"},                                   // missed
		{pid: 14, exe: "/usr/bin/python3.12", entry: "-I"},                                             // missed
		{pid: 15, exe: "/usr/bin/python3.12", entry: "/app/a.py", optedOut: true},                      // opted out
		{pid: 16, exe: "/usr/local/bin/node", entry: "/srv/sidecar.js", ownRoot: true},                 // sidecar
		{pid: 17, exe: "/usr/bin/java"},                                                                // not ours
		{pid: 18, exe: "/root/.bun/bin/bun"},                                                           // second bun
		{pid: 19, exe: "/usr/local/bin/node", entry: "/usr/lib/node_modules/corepack/dist/pnpm.js"},    // launcher
	}
	n, runtimes := unregisteredRuntimes(candidates, map[int]bool{10: true})
	if n != 5 {
		t.Fatalf("count = %d, want 5 (pids 12, 13, 14, 16, 18)", n)
	}
	if want := []string{"bun", "node", "node-sidecar", "python"}; !reflect.DeepEqual(runtimes, want) {
		t.Fatalf("runtimes = %v, want %v", runtimes, want)
	}
	if n, runtimes := unregisteredRuntimes(candidates[:2], map[int]bool{10: true}); n != 0 || len(runtimes) != 0 {
		t.Fatalf("covered guest reported %d %v", n, runtimes)
	}
}

func TestRegisteredPIDs(t *testing.T) {
	b := newRestoreReseedBarrier(nil)
	b.clients[&restoreReseedClient{pid: 42, runtime: "node"}] = struct{}{}
	b.clients[&restoreReseedClient{pid: 43, runtime: "python"}] = struct{}{}
	if got := b.registeredPIDs(); !reflect.DeepEqual(got, map[int]bool{42: true, 43: true}) {
		t.Fatalf("registeredPIDs = %v", got)
	}
}
