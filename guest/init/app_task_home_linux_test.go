//go:build linux

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// A release command or app task gets a writable HOME like the app process;
// with HOME=/ mise logged "Permission denied" in every release log (SB-10).
// That is the user's passwd home when the user can write there, as under
// Docker (ADR-20261025111339), else /tmp.
func TestPrepareAppTaskCommandHasWritableHome(t *testing.T) {
	t.Setenv("HOME", "/")
	cmd, credential, result := prepareAppTaskCommand(apptaskproto.Request{Command: []string{"/bin/true"}}, api.AppManifest{}, nil, nil)
	if result != nil {
		t.Fatalf("prepareAppTaskCommand: %+v", *result)
	}
	home := ""
	for _, kv := range cmd.Env {
		if k, v, ok := cut(kv); ok && k == "HOME" {
			home = v
		}
	}
	var uid, gid uint32 // a nil credential keeps root's identity
	if credential != nil {
		uid, gid = credential.Uid, credential.Gid
	}
	want := workloadHome(uid, gid)
	if want == "" {
		want = defaultWorkloadHome
	}
	if home == "/" || home != want {
		t.Fatalf("task HOME = %q, want %q", home, want)
	}
}
