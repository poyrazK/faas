package main

import (
	"context"
	"flag"
	"os"
)

// cmdCronsRm deletes one cron rule. It never prompts, so --yes is accepted as
// a no-op for scripts that pass it to every rm verb; any other flag is named
// as unknown instead of falling through to a bare usage line (H10-97).
func cmdCronsRm(args []string) int {
	fs := newFlagSet("crons-rm", flag.ContinueOnError)
	fs.Bool("yes", false, "accepted for scripts; crons rm does not prompt")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale cron delete <id>", "crons")
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteCron(context.Background(), id); err != nil {
		return printErr("Could not delete cron", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"id":      id,
			"status":  "deleted",
			"deleted": true,
		}))
	}
	PrintOK(osStdout, "Removed")
	return 0
}
