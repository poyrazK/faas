package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// prod 2026-11-20: `cron runs <app> <id>` and `domain add <app> <domain>`
// printed only a usage line; each now names the form that works.
func TestPositionalMistakesNameTheWorkingForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() int
		want string
	}{
		{"cron runs with app", func() int {
			return main.CmdCronsRuns([]string{"my-app", "5a470eed-f718-48a2-b3fa-1ad6a6b10c18"})
		}, "gregale cron runs 5a470eed-f718-48a2-b3fa-1ad6a6b10c18"},
		// Prod hunt r31: `cron runs APP` printed only the usage line and
		// `--app` alone answered "--app requires --interactive".
		{"cron runs app only", func() int {
			return main.CmdCronsRuns([]string{"my-app"})
		}, "gregale cron list my-app"},
		{"cron runs short id is not an app", func() int {
			return main.CmdCronsRuns([]string{"5a470eed"})
		}, "usage: gregale cron runs <id>"},
		{"cron runs --app without id", func() int {
			return main.CmdCronsRuns([]string{"--app", "my-app"})
		}, "gregale cron list my-app"},
		{"domain add app first", func() int {
			return main.Run([]string{"domain", "add", "my-app", "shop.example.com"})
		}, "gregale domain create shop.example.com --app my-app"},
		{"domain add domain first", func() int {
			return main.Run([]string{"domain", "add", "shop.example.com", "my-app"})
		}, "gregale domain create shop.example.com --app my-app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, restore := main.SwapIO(t)
			code := tc.run()
			restore()
			if got := stderr(); code == 0 || !strings.Contains(got, tc.want) {
				t.Fatalf("exit %d stderr %q, want it to contain %q", code, got, tc.want)
			}
		})
	}
}
