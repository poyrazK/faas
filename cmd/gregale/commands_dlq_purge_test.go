package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"testing"
)

// hunt #8 (H8-22): `dlq purge --all` must confirm before deleting every dead
// letter, and accept the --yes other destructive commands use.
func TestDLQPurgeAllConfirmsUnlessYes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		want  int
		calls bool
	}{
		{name: "declined prompt sends nothing", args: []string{"demo", "--all"}, stdin: "nope\n", want: 1},
		{name: "typed confirmation purges", args: []string{"demo", "--all"}, stdin: "purge dead letters\n", want: 0, calls: true},
		{name: "--yes skips the prompt", args: []string{"demo", "--all", "--yes"}, want: 0, calls: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			main.ResetJSONOut(t)
			api := main.AuthedFakeAPI(t, `{"purged":0}`, http.StatusOK)
			main.PipeStdin(t, tc.stdin)
			if code := main.CmdDLQPurge(tc.args); code != tc.want {
				t.Fatalf("exit = %d, want %d", code, tc.want)
			}
			if called := (*api.SawPath()) != ""; called != tc.calls {
				t.Fatalf("API called = %v (%s %s), want %v", called, (*api.SawMethod()), (*api.SawPath()), tc.calls)
			}
		})
	}
}
