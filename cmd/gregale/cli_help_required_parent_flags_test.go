package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale flags get --help` listed no flags, then the
// command answered "--project is required". Subcommand help and
// docs/cli-reference.md render only the subcommand's own flags, so a flag the
// parent marks required must be declared on every subcommand too (Hunt 14
// H14-24).
func TestSubcommandHelpListsRequiredParentFlags(t *testing.T) {
	for _, command := range main.CustomerCliCommands() {
		for _, parent := range command.Flags {
			if !parent.Req {
				continue
			}
			for _, sub := range command.Subcommands {
				declared := false
				for _, flag := range sub.Flags {
					declared = declared || flag.Name == parent.Name
				}
				if !declared {
					t.Errorf("gregale %s %s --help omits the required --%s", command.Name, sub.Name, parent.Name)
				}
			}
		}
	}
}
