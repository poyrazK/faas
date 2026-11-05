//go:build linux

package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR-20261025134512: the selected Go command reaches `railpack prepare`,
// and BuildKit gets the plan secret Railpack derives from it.
func TestBuildArgvSelectsGoCommand(t *testing.T) {
	for _, cache := range []bool{false, true} {
		argv := buildArgv(api.BuildManifest{Framework: api.FrameworkRailpackGo, Workdir: "/build/src", OutDir: "/build/out", GoCommand: "worker", DependencyCache: cache})
		script := argv[len(argv)-1]
		for _, want := range []string{
			"set -x; export RAILPACK_GO_BIN='worker'; /usr/local/bin/railpack prepare '/build/src' ",
			"--info-out '/build/railpack-info.json' --env 'RAILPACK_GO_BIN=worker' &&",
			" --secret id=RAILPACK_GO_BIN,env=RAILPACK_GO_BIN",
		} {
			if !strings.Contains(script, want) {
				t.Fatalf("cache=%t: build script misses %q:\n%s", cache, want, script)
			}
		}
	}
	plain := buildArgv(api.BuildManifest{Framework: api.FrameworkRailpackGo, Workdir: "/build/src", OutDir: "/build/out"})
	if script := plain[len(plain)-1]; strings.Contains(script, "RAILPACK_GO_BIN") {
		t.Fatalf("a build without a Go command selects one:\n%s", script)
	}
}

func TestGoCommandAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest api.BuildManifest
		wantErr  bool
		ack      string
	}{
		{"railpack go build", api.BuildManifest{Framework: api.FrameworkRailpackGo, GoCommand: "worker"}, false, "worker"},
		{"no command", api.BuildManifest{Framework: api.FrameworkRailpackGo}, false, ""},
		{"Dockerfile build", api.BuildManifest{Framework: api.FrameworkDockerfile, GoCommand: "worker"}, true, ""},
		{"watch-mode build", api.BuildManifest{Framework: api.FrameworkRailpackGo, GoCommand: "worker", DevWatchCommand: "go run ./cmd/worker"}, true, ""},
		{"unsafe name", api.BuildManifest{Framework: api.FrameworkRailpackGo, GoCommand: "w'; rm -rf /"}, true, ""},
	} {
		if err := checkGoCommand(tc.manifest); (err != nil) != tc.wantErr {
			t.Errorf("%s: check = %v, want error %t", tc.name, err, tc.wantErr)
		}
		if got := goCommandAcknowledgement(tc.manifest); got != tc.ack {
			t.Errorf("%s: acknowledgement = %q, want %q", tc.name, got, tc.ack)
		}
	}
}
