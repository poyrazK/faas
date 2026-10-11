package main

import (
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
			resetJSONOut(t)
			api := authedFakeAPI(t, `{"purged":0}`, http.StatusOK)
			pipeStdin(t, tc.stdin)
			if code := cmdDLQPurge(tc.args); code != tc.want {
				t.Fatalf("exit = %d, want %d", code, tc.want)
			}
			if called := api.sawPath != ""; called != tc.calls {
				t.Fatalf("API called = %v (%s %s), want %v", called, api.sawMethod, api.sawPath, tc.calls)
			}
		})
	}
}
