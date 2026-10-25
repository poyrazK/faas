package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale alerts get` answered only "unknown alerts
// subcommand" with no verb to try; 26 dispatchers printed their own bare line
// (Hunt 14 H14-51).
func TestDispatchersNameTheirVerbsAfterAnUnknownSubcommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		run     func([]string) int
		arg     string
		want    []string
		without []string
	}{
		{"a verb word lists the verbs", main.CmdAlerts, "get", []string{`unknown alerts subcommand "get"`, "Subcommands: actions, list,"}, []string{"comes first"}},
		{"an app slug gets the verb-first form", main.CmdCrons, "h14-py", []string{"Subcommands: next, list,", "If h14-py is an app, the subcommand comes first: gregale crons list h14-py"}, nil},
		{"a typo gets did-you-mean", main.CmdAlerts, "listz", []string{`Did you mean "list"`}, []string{"Subcommands:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr, restore := main.CaptureStderr(t)
			code := tc.run([]string{tc.arg})
			restore()
			got := stderr.String()
			for _, want := range tc.want {
				if code != 1 || !strings.Contains(got, want) {
					t.Fatalf("exit %d; missing %q:\n%s", code, want, got)
				}
			}
			for _, unwanted := range tc.without {
				if strings.Contains(got, unwanted) {
					t.Fatalf("unexpected %q:\n%s", unwanted, got)
				}
			}
		})
	}
}
