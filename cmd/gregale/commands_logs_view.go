package main

// `gregale logs view` — ADR-20261026145832: one place for saved log
// filters. Local views (`logs views`), server-side saved searches
// (`logs saved`, here `--shared`) and the preferred view (`logs defaults`)
// were three commands; the stores stay as they are.

import (
	"fmt"
	"os"
	"slices"
)

const logsViewUsage = "usage: gregale logs view <list|show|save|delete|rename|copy|update|use> [--shared] [args]"

func cmdLogsView(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, logsViewUsage, "logs")
		return 1
	}
	verb, rest := args[0], args[1:]
	shared := false
	if i := slices.Index(rest, "--shared"); i >= 0 {
		shared = true
		rest = slices.Delete(slices.Clone(rest), i, i+1)
	}
	if shared {
		switch verb {
		case subList, "save", "delete":
			return cmdLogsSaved(append([]string{verb}, rest...))
		}
		_, _ = fmt.Fprintf(osStderr, "--shared views support list, save and delete; %q works on local views\n", verb)
		return 1
	}
	if verb == "use" {
		i := slices.Index(rest, "--default")
		if i < 0 {
			_, _ = fmt.Fprintln(osStderr, "use a view for one query with: gregale logs <app> --view NAME; make it the default with: gregale logs view use NAME --default")
			return 1
		}
		return cmdLogsDefaults(append([]string{"set"}, slices.Delete(slices.Clone(rest), i, i+1)...))
	}
	return cmdLogViews(args)
}
