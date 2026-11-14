//go:build linux

package main

// adr: 20261027090013 §4 — sandbox.json alone selects sandbox mode, and PID
// 1's reaper hands every command its own exit, even one that ended before
// its command asked.

import (
	"testing"
	"testing/fstest"
)

func TestDecideModeSandbox(t *testing.T) {
	manifest := &fstest.MapFile{Data: []byte(`{"kind":"sandbox","version":1}`)}
	mode, _, err := decideMode(fstest.MapFS{sandboxManifestRelativePath: manifest})
	if err != nil || mode != modeSandbox {
		t.Fatalf("sandbox.json alone: %v %v", mode, err)
	}
	for name, fsys := range map[string]fstest.MapFS{
		"with an agent manifest": {sandboxManifestRelativePath: manifest, agentManifestRelativePath: {Data: []byte(`{"kind":"agent","version":1}`)}},
		"with an app task":       {sandboxManifestRelativePath: manifest, appTaskManifestRelativePath: {Data: []byte(`{}`)}},
		"with a job":             {sandboxManifestRelativePath: manifest, "etc/faas/job.json": {Data: []byte(`{"kind":"job"}`)}},
		"invalid":                {sandboxManifestRelativePath: {Data: []byte(`{"kind":"sandbox"}`)}},
	} {
		if _, _, err := decideMode(fsys); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSandboxReaperHandsOutExits(t *testing.T) {
	r := &sandboxReaper{waiters: map[int]chan sandboxExit{}, early: map[int]sandboxExit{}, wake: make(chan struct{}, 1)}
	waiting := r.wait(10)
	r.deliver(10, sandboxExit{code: 3})
	if got := <-waiting; got.code != 3 {
		t.Fatalf("a waiting command got %+v", got)
	}
	r.deliver(11, sandboxExit{signal: 9, code: 137})
	if got := <-r.wait(11); got.signal != 9 {
		t.Fatalf("an exit before its wait got %+v", got)
	}
	for pid := 100; pid < 100+maxEarlyExits+10; pid++ {
		r.deliver(pid, sandboxExit{})
	}
	if len(r.early) > maxEarlyExits {
		t.Fatalf("%d exits kept for no command", len(r.early))
	}
}
