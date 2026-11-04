package main_test

import (
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `gregale tail h12-q` failed with a usage error although logs, ps and
// invoke take the slug positionally (Hunt 12 H12-45). Without a token a
// parsed command stops at authentication (exit 2); a usage error exits 1.
func TestTailAcceptsPositionalSlug(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{"positional slug", []string{"web"}, 2},
		{"--app", []string{"--app", "web"}, 2},
		{"same app twice", []string{"web", "--app", "web"}, 2},
		{"two different apps", []string{"web", "--app", "api"}, 1},
		{"two positionals", []string{"web", "api"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			t.Chdir(t.TempDir())
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("FAAS_TOKEN", "")
			t.Setenv("FAAS_API", "http://127.0.0.1:1")
			_, _, restore := main.SwapIO(t)
			code := main.CmdTail(tt.args)
			restore()
			if code != tt.want {
				t.Fatalf("tail %v exit = %d, want %d", tt.args, code, tt.want)
			}
		})
	}
}
