package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStampDevDebugEnv(t *testing.T) {
	cases := []struct {
		name      string
		env       []string
		preload   string
		notDevApp bool
		want      []string
		active    bool
	}{
		{name: "not debugging", env: []string{"PATH=/bin"}, want: []string{"PATH=/bin"}},
		{name: "unsupported runtime", env: []string{"FAAS_DEV_DEBUG=python"}, want: []string{"FAAS_DEV_DEBUG=python"}},
		{name: "node without options", env: []string{"FAAS_DEV_DEBUG=node"}, preload: "/run/p.cjs",
			want: []string{"FAAS_DEV_DEBUG=node", "NODE_OPTIONS=--require=/run/p.cjs"}, active: true},
		{name: "node keeps existing options", env: []string{"NODE_OPTIONS=--max-old-space-size=200", "FAAS_DEV_DEBUG=node"}, preload: "/run/p.cjs",
			want: []string{"FAAS_DEV_DEBUG=node", "NODE_OPTIONS=--max-old-space-size=200 --require=/run/p.cjs"}, active: true},
		{name: "no preload falls back to inspect", env: []string{"FAAS_DEV_DEBUG=node"},
			want: []string{"FAAS_DEV_DEBUG=node", "NODE_OPTIONS=--inspect=0.0.0.0:9229"}, active: true},
		{name: "app inspect on the tunnel port", env: []string{"NODE_OPTIONS=--inspect=0.0.0.0:9229", "FAAS_DEV_DEBUG=node"},
			want: []string{"NODE_OPTIONS=--inspect=0.0.0.0:9229", "FAAS_DEV_DEBUG=node"}, active: true},
		// The tunnel cannot reach an inspector elsewhere, so liveness stays
		// ordinary.
		{name: "app inspect elsewhere", env: []string{"NODE_OPTIONS=--inspect=0.0.0.0:9339", "FAAS_DEV_DEBUG=node"},
			want: []string{"NODE_OPTIONS=--inspect=0.0.0.0:9339", "FAAS_DEV_DEBUG=node"}},
		{name: "app inspect on loopback", env: []string{"NODE_OPTIONS=--inspect=127.0.0.1:9229", "FAAS_DEV_DEBUG=node"},
			want: []string{"NODE_OPTIONS=--inspect=127.0.0.1:9229", "FAAS_DEV_DEBUG=node"}},
		// The switch set on a production app (env, secret or manifest) is
		// ignored: only schedd's developer marker enables it.
		{name: "not a developer environment", env: []string{"FAAS_DEV_DEBUG=node"}, preload: "/run/p.cjs", notDevApp: true,
			want: []string{"FAAS_DEV_DEBUG=node"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			devDebugActive.Store(false)
			if got := StampDevDebugEnv(tc.env, tc.preload, !tc.notDevApp); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("env = %v, want %v", got, tc.want)
			}
			if devDebugActive.Load() != tc.active {
				t.Fatalf("debug active = %t, want %t", devDebugActive.Load(), tc.active)
			}
		})
	}
	devDebugActive.Store(false)
}

func TestDevDebugAttached(t *testing.T) {
	dir := t.TempDir()
	header := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	listen := "   0: 00000000:240D 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 101 1 0 100 0 0 10 0\n"
	established := "   1: 0200000A:240D 0100000A:C350 01 00000000:00000000 00:00000000 00000000     0        0 102 1 0 20 4 30 10 -1\n"
	appConn := "   2: 0200000A:1F90 0100000A:C351 01 00000000:00000000 00:00000000 00000000     0        0 103 1 0 20 4 30 10 -1\n"
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(header+body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	old := devDebugTCPTables
	t.Cleanup(func() { devDebugTCPTables = old })

	devDebugTCPTables = []string{write("listen", listen+appConn), filepath.Join(dir, "missing")}
	if devDebugAttached() {
		t.Fatal("a listening inspector with no client reported a debugger attached")
	}
	devDebugTCPTables = []string{write("tcp", listen), write("tcp6", established)}
	if !devDebugAttached() {
		t.Fatal("an established inspector connection was not reported attached")
	}
}

// TestDevDebugPreloadSkipsSupervisors runs the preload under a real Node.js
// when one is installed: watchers and package managers must not take the
// inspector port, the app's own script must.
func TestDevDebugPreloadSkipsSupervisors(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	preload := filepath.Join(dir, "preload.cjs")
	if err := writeDevDebugPreload(preload); err != nil {
		t.Fatal(err)
	}
	report := `process.stdout.write(require('inspector').url() ? 'open' : 'closed'); require('inspector').close();`
	run := func(rel string, execArgs ...string) string {
		t.Helper()
		script := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(script, []byte(report), 0o644); err != nil {
			t.Fatal(err)
		}
		args := append(append([]string{"--require=" + preload}, execArgs...), script)
		cmd := exec.Command(node, args...)
		cmd.Env = append(os.Environ(), "NODE_OPTIONS=")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("node %v: %v", args, err)
		}
		return string(out)
	}
	if got := run("app/server.js"); got != "open" {
		t.Skipf("inspector port %d unavailable on this machine (%s)", 9229, got)
	}
	for _, rel := range []string{
		"node_modules/nodemon/bin/nodemon.js",
		"node_modules/tsx/dist/cli.mjs",
		"node_modules/ts-node-dev/lib/bin.js",
		"node_modules/next/dist/bin/next",
		"node_modules/.bin/next",
		"node_modules/npm/bin/npm-cli.js",
	} {
		if strings.HasSuffix(rel, ".mjs") {
			continue // an ES module cannot use require; the path match is what matters
		}
		if got := run(rel); got != "closed" {
			t.Errorf("%s took the inspector port", rel)
		}
	}
}
