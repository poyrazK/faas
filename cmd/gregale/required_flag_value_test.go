package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

// Hunt #9: `realtime delete --help` advertised `--yes <value>` because the
// required switch was not marked Bool, so help invented a value placeholder
// for a flag the parser reads as a boolean. A required flag with no Value and
// no ClosedSet can only be a switch; it must say so.
func TestRequiredFlagsWithoutValueAreMarkedBool(t *testing.T) {
	var walk func(path string, flags []main.CliFlag, subs []main.CliSub)
	walk = func(path string, flags []main.CliFlag, subs []main.CliSub) {
		for _, f := range flags {
			if f.Req && !f.Bool && f.Value == "" && len(f.ClosedSet) == 0 {
				t.Errorf("%s --%s is required with no Value: mark it Bool or name its value", path, f.Name)
			}
		}
		for _, sub := range subs {
			walk(path+" "+sub.Name, sub.Flags, sub.Subcommands)
		}
	}
	for _, c := range *main.CliCommands {
		walk("gregale "+c.Name, c.Flags, c.Subcommands)
	}
}
