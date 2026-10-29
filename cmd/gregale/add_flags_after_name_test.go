package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale add bucket --app h14-py h14-files --env
// default ...`, the order the help shows, answered "--env is required"
// because flag parsing stopped at the bucket name and dropped every flag
// after it (Hunt 14 H14-13).
func TestAddParsesFlagsAfterTheName(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func([]string) int
		args []string
		want string
	}{
		{"bucket", main.CmdAddBucket, []string{"--app", "my-api", "files", "--env", "default", "--permission", "bogus"}, "usage: gregale add bucket"},
		{"postgres", main.CmdAddPostgres, []string{"--app", "my-api", "db", "--env", "default", "--class", "bogus"}, "--class"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			main.WithCwd(t, t.TempDir())
			stderr, restore := main.CaptureStderr(t)
			code := tc.run(tc.args)
			restore()
			if code != 1 || strings.Contains(stderr.String(), "--env is required") || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit = %d; flags after the name must be parsed (want %q, no --env error): %s", code, tc.want, stderr.String())
			}
		})
	}
}
