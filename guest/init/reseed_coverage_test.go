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
		"/usr/bin/java": "java", "/opt/java/openjdk/bin/java": "java",
		"/app/server": "", "/usr/bin/python3-config": "", "/bin/sh": "", "/usr/bin/javac": "",
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
		{pid: 17, exe: "/usr/bin/javac"},                                                               // not ours
		{pid: 18, exe: "/root/.bun/bin/bun"},                                                           // second bun
		{pid: 19, exe: "/usr/local/bin/node", entry: "/usr/lib/node_modules/corepack/dist/pnpm.js"},    // launcher
	}
	// ADR-20261110150000: a JVM that did not register, and a Go workload
	// (the scanner sets runtime "go" from the executable's build info).
	candidates = append(candidates,
		reseedCandidate{pid: 20, exe: "/opt/java/openjdk/bin/java"}, // missed (JVM < 16)
		reseedCandidate{pid: 21, exe: "/opt/java/openjdk/bin/java"}, // registered agent
		reseedCandidate{pid: 22, exe: "/app/server", runtime: "go"}, // missed
		reseedCandidate{pid: 23, exe: "/app/worker", runtime: "go"}, // imports the helper
	)
	n, runtimes := unregisteredRuntimes(candidates, map[int]bool{10: true, 21: true, 23: true})
	if n != 7 {
		t.Fatalf("count = %d, want 7 (pids 12, 13, 14, 16, 18, 20, 22)", n)
	}
	if want := []string{"bun", "go", "java", "node", "node-sidecar", "python"}; !reflect.DeepEqual(runtimes, want) {
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

// ADR-20261110170000: Go ID libraries keep per-process random state.
func TestGoIDLibrary(t *testing.T) {
	for path, want := range map[string]string{
		"github.com/rs/xid":                          "github.com/rs/xid",
		"go.mongodb.org/mongo-driver":                "go.mongodb.org/mongo-driver",
		"go.mongodb.org/mongo-driver/v2":             "go.mongodb.org/mongo-driver",
		"go.opentelemetry.io/otel/sdk":               "go.opentelemetry.io/otel/sdk",
		"go.opentelemetry.io/otel/sdk/metric":        "go.opentelemetry.io/otel/sdk",
		"go.opentelemetry.io/otel":                   "",
		"github.com/rs/xidx":                         "",
		"github.com/google/uuid":                     "",
		"go.mongodb.org/mongo-driver-something-else": "",
	} {
		if got := goIDLibrary(path); got != want {
			t.Errorf("goIDLibrary(%q) = %q, want %q", path, got, want)
		}
	}
	if got := sortedKeys(map[string]bool{"b": true, "a": true}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("sortedKeys = %v", got)
	}
}
