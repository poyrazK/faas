package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// boundDatabaseNames lists, once each and sorted, the databases bindings
// point at.
func boundDatabaseNames(bindings []api.AppPostgresBinding) []string {
	seen := map[string]bool{}
	var names []string
	for _, binding := range bindings {
		if binding.Database != "" && !seen[binding.Database] {
			seen[binding.Database] = true
			names = append(names, binding.Database)
		}
	}
	sort.Strings(names)
	return names
}

// printKeptDatabases says that revoking an app's bindings left its databases
// in place. Deleting a Compose project's apps left the databases Compose had
// created without a word (prod 2026-11-19).
func printKeptDatabases(w io.Writer, names []string) {
	if len(names) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "These databases were kept, with their data:")
	for _, name := range names {
		_, _ = fmt.Fprintf(w, "  %s    delete with: %s postgres delete %s\n", name, recoveryCLI(), recoveryArgument(name))
	}
}
