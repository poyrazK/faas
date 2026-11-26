package sharedruntime_test

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/sharedruntime"
)

// Prod hunt r57: busybox sh ran `node worker.js` as its child, so the stop
// SIGTERM reached the shell and the worker never drained. A simple command
// is exec'd; anything that is more than one command keeps the shell.
func TestShellStartExecsSimpleCommands(t *testing.T) {
	tests := []struct {
		command, want string
	}{
		{"node worker.js", "exec node worker.js"},
		{"  node --watch worker.js ", "exec node --watch worker.js"},
		{"gunicorn --workers $PORT app:app", "exec gunicorn --workers $PORT app:app"},
		{"node server.js > /tmp/log", "exec node server.js > /tmp/log"},
		{"node server.js > /tmp/log 2>&1", "node server.js > /tmp/log 2>&1"},
		{"exec node worker.js", "exec node worker.js"},
		{"PORT=3000 bun run start", "PORT=3000 bun run start"},
		{"cd src && node server.js", "cd src && node server.js"},
		{"node a.js; node b.js", "node a.js; node b.js"},
		{"node a.js | tee log", "node a.js | tee log"},
		{"node a.js &", "node a.js &"},
		{"gunicorn --workers $(nproc) app:app", "gunicorn --workers $(nproc) app:app"},
		{"node a.js\nnode b.js", "node a.js\nnode b.js"},
		{"if true; then node a.js; fi", "if true; then node a.js; fi"},
		{"export X=1", "export X=1"},
		{". ./env.sh", ". ./env.sh"},
	}
	for _, tc := range tests {
		got := sharedruntime.ShellStart(tc.command)
		if want := []string{"/bin/sh", "-c", tc.want}; !slices.Equal(got, want) {
			t.Errorf("ShellStart(%q) = %q, want %q", tc.command, got, want)
		}
	}
}
