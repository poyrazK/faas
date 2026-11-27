package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresBindingTimeouts(args []string) int {
	const command = "postgres bindings timeouts"
	fs := newFlagSet(command, flag.ContinueOnError)
	var request api.SetManagedPostgresBindingTimeoutsRequest
	duration := func(name, description string, target **int32) {
		fs.Func(name, description, func(value string) error {
			d, err := time.ParseDuration(value)
			if err != nil || d < 0 || d%time.Millisecond != 0 || d.Milliseconds() > math.MaxInt32 {
				return fmt.Errorf("use a duration from 0 to %s in whole milliseconds", time.Duration(math.MaxInt32)*time.Millisecond)
			}
			ms := int32(d.Milliseconds())
			*target = &ms
			return nil
		})
	}
	duration("statement", "statement timeout duration (0 disables)", &request.StatementMS)
	duration("lock", "lock timeout duration (0 disables)", &request.LockMS)
	duration("idle-in-transaction", "idle transaction timeout duration (0 disables)", &request.IdleInTransactionMS)
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale "+command+" BINDING_ID [--statement DURATION] [--lock DURATION] [--idle-in-transaction DURATION]", "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	id, org := fs.Arg(0), client.ProjectOrganization()
	var status api.ManagedPostgresBindingTimeouts
	switch {
	case fs.NFlag() == 0 && org != "":
		status, err = client.GetOrgManagedPostgresBindingTimeouts(ctx, org, id)
	case fs.NFlag() == 0:
		status, err = client.GetManagedPostgresBindingTimeouts(ctx, id)
	case org != "":
		status, err = client.SetOrgManagedPostgresBindingTimeouts(ctx, org, id, request)
	default:
		status, err = client.SetManagedPostgresBindingTimeouts(ctx, id, request)
	}
	if err != nil {
		return printErr("Could not manage PostgreSQL binding timeouts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	_, _ = fmt.Fprintf(osStdout, "State\t%s\nStatement timeout\t%s\nLock timeout\t%s\nIdle transaction timeout\t%s\n",
		status.State, renderBindingTimeout(status.StatementMS), renderBindingTimeout(status.LockMS),
		renderBindingTimeout(status.IdleInTransactionMS))
	return 0
}

func renderBindingTimeout(ms int32) string {
	if ms == 0 {
		return "disabled"
	}
	return (time.Duration(ms) * time.Millisecond).String()
}
