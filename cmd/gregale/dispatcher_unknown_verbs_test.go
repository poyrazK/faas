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
		{"a verb word lists the verbs", main.CmdAlerts, "describe", []string{`unknown alerts subcommand "describe"`, "Subcommands: actions, list,"}, []string{"comes first"}},
		{"an app slug gets the verb-first form", main.CmdCrons, "h14-py", []string{"Subcommands: next, list,", "If h14-py is an app, the subcommand comes first: gregale cron list --app h14-py"}, nil},
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

// Prod rc.369: `gregale cron history` listed next, add, info, rm while `cron
// --help` lists create, get, delete; domain, key, alert and webhook did the
// same (ADR-20261026145832). A typed tree noun lists the verbs its help
// lists; today's noun keeps its own verbs, and a tree verb typed after it
// names the tree command that runs it.
func TestUnknownSubcommandChoicesFollowTheTypedNoun(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		want    []string
		without []string
	}{
		{[]string{"cron", "history"}, []string{"Subcommands: ", " create,", " get,", " delete,"}, []string{" add,", " info,", " rm,"}},
		{[]string{"cron", "creat"}, []string{`Did you mean "create"`}, nil},
		{[]string{"domain", "zzz"}, []string{"Subcommands: ", " create,", " delete,"}, []string{" add,", " rm,"}},
		{[]string{"crons", "history"}, []string{"Subcommands: ", " add,", " info,"}, []string{" create,"}},
		{[]string{"alerts", "get"}, []string{`Did you mean 'gregale alert get'?`}, nil},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			main.ResetJSONOut(t)
			_, stderr, restore := main.SwapIO(t)
			code := main.Run(tc.args)
			restore()
			got := stderr()
			if code != 1 {
				t.Fatalf("exit = %d, want 1; stderr=%q", code, got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("stderr lacks %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tc.without {
				if strings.Contains(got, unwanted) {
					t.Errorf("stderr has %q:\n%s", unwanted, got)
				}
			}
		})
	}
}
