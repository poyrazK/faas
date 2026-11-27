package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPostgresAutoGrow(args []string) int {
	const usage = "usage: gregale [--org ORG] postgres auto-grow DATABASE (--max-bytes BYTES | --off)"
	fs := newFlagSet("postgres auto-grow", flag.ContinueOnError)
	maxBytes := fs.Int64("max-bytes", 0, "per-volume storage ceiling in bytes")
	off := fs.Bool("off", false, "disable automatic growth")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || *maxBytes < 0 || *off == (*maxBytes > 0) {
		PrintUsage(os.Stderr, usage, "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := postgresConditions.resolveRef(ctx, client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	result, err := client.SetManagedPostgresAutoGrow(ctx, id, api.SetManagedPostgresAutoGrowRequest{MaxBytes: *maxBytes})
	if err != nil {
		return printErr("Could not set automatic storage growth", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	renderPostgresAutoGrow(osStdout, result)
	return 0
}

func renderPostgresAutoGrow(w io.Writer, status api.ManagedPostgresAutoGrowStatus) {
	_, _ = fmt.Fprintf(w, "  Auto-grow: %s", strings.ReplaceAll(status.Reason, "_", " "))
	if status.MaxBytes > 0 {
		_, _ = fmt.Fprintf(w, " (ceiling %s)", formatPostgresStorage(status.MaxBytes))
	}
	if status.ProposedBytes > 0 {
		_, _ = fmt.Fprintf(w, ", proposed %s", formatPostgresStorage(status.ProposedBytes))
	}
	if status.LastGrownAt != nil {
		_, _ = fmt.Fprintf(w, ", last grew %s", status.LastGrownAt.Format(time.RFC3339))
	}
	if status.ResizeID != "" {
		_, _ = fmt.Fprintf(w, ", resize %s", status.ResizeID)
	}
	_, _ = fmt.Fprintln(w)
	if status.Reason == "manual_resize_required" || status.Reason == "ceiling_reached" {
		_, _ = fmt.Fprintln(w, "  Manual storage growth: gregale postgres resize DATABASE --storage-bytes BYTES --request-id UUID")
	}
}
