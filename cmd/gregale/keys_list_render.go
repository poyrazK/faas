package main

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

// renderAPIKeys prints `key list` as a table. The ID column is what `key
// delete` takes, so a leaked key can be revoked straight from this output;
// created and expires say which keys are old or about to lapse.
func renderAPIKeys(w io.Writer, keys []api.APIKeyResponse) {
	if len(keys) == 0 {
		_, _ = fmt.Fprintln(w, "(no API keys)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLABEL\tPREFIX\tSTATUS\tCREATED\tEXPIRES")
	for _, k := range keys {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			keyListCell(k.ID), keyListCell(k.Label), keyListCell(k.Prefix), keyListCell(k.Status),
			keyListCell(activityDisplayTime(k.CreatedAt)), keyListCell(activityDisplayTime(k.ExpiresAt)))
	}
	_ = tw.Flush()
}

// keyListCell keeps a server- or user-supplied value on one table cell.
func keyListCell(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return "-"
	}
	return terminalSafe(s)
}
