package main

import (
	"flag"
	"fmt"
	"io"
)

type migrationOptions struct {
	Status, Leader, Wait bool
	Recovery             string
	Approval             string
}

func parseMigrationOptions(args []string, output io.Writer) (migrationOptions, error) {
	var options migrationOptions
	var prepare, preview bool
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.BoolVar(&options.Status, "status", false, "report migration status without applying")
	flags.BoolVar(&options.Leader, "leader", false, "apply as the cluster migration leader")
	flags.BoolVar(&options.Wait, "wait-for-migrations", false, "wait for every embedded migration without applying")
	flags.BoolVar(&prepare, "prepare-ledger-recovery", false, "apply only the additive standards recovery audit migration")
	flags.BoolVar(&preview, "ledger-recovery-plan", false, "verify schema/backfills and print an exact standards ledger repair plan")
	flags.StringVar(&options.Approval, "ledger-recovery-apply", "", "apply only the reviewed ledger plan matching this SHA-256 hash")
	if err := flags.Parse(args); err != nil {
		return options, err
	}
	if flags.NArg() != 0 {
		return options, fmt.Errorf("migrate: unexpected positional arguments")
	}
	apply := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "ledger-recovery-apply" {
			apply = true
		}
	})
	if apply && options.Approval == "" {
		return options, fmt.Errorf("migrate: ledger recovery requires an approval hash")
	}
	count := 0
	for name, enabled := range map[string]bool{"prepare": prepare, "preview": preview, "apply": apply} {
		if enabled {
			count++
			options.Recovery = name
		}
	}
	if count > 1 || count > 0 && (options.Status || options.Leader || options.Wait) {
		return options, fmt.Errorf("migrate: recovery modes are mutually exclusive with other modes")
	}
	if options.Wait && (options.Status || options.Leader) {
		return options, fmt.Errorf("migrate: waiter is mutually exclusive with status and leader")
	}
	return options, nil
}
