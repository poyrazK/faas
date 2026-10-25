package main_test

import (
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale app --help` called --max-concurrency
// "maximum concurrent requests"; it caps the app's instances (spec §6.2-1),
// so a customer raising it to scale a slow app saw no change in per-instance
// concurrency (Hunt 14 H14-39).
func TestMaxConcurrencyHelpNamesTheInstanceCap(t *testing.T) {
	found := 0
	for _, command := range main.CustomerCliCommands() {
		if command.Name != "app" {
			continue
		}
		check := func(where, short string) {
			found++
			if !strings.Contains(short, "instances") || strings.Contains(short, "concurrent requests") {
				t.Errorf("%s --max-concurrency help = %q, want the instance cap", where, short)
			}
		}
		for _, flag := range command.Flags {
			if flag.Name == "max-concurrency" {
				check("app", flag.Short)
			}
		}
		for _, sub := range command.Subcommands {
			for _, flag := range sub.Flags {
				if flag.Name == "max-concurrency" {
					check("app "+sub.Name, flag.Short)
				}
			}
		}
	}
	if found == 0 {
		t.Fatal("no --max-concurrency flag found under gregale app")
	}
}
