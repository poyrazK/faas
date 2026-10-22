//go:build linux

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

// A release command or app task gets a writable HOME like the app process;
// with HOME=/ mise logged "Permission denied" in every release log (SB-10).
func TestPrepareAppTaskCommandHasWritableHome(t *testing.T) {
	t.Setenv("HOME", "/")
	cmd, _, result := prepareAppTaskCommand(apptaskproto.Request{Command: []string{"/bin/true"}}, api.AppManifest{}, nil, nil)
	if result != nil {
		t.Fatalf("prepareAppTaskCommand: %+v", *result)
	}
	home := ""
	for _, kv := range cmd.Env {
		if k, v, ok := cut(kv); ok && k == "HOME" {
			home = v
		}
	}
	if home != defaultWorkloadHome {
		t.Fatalf("task HOME = %q, want %q", home, defaultWorkloadHome)
	}
}
