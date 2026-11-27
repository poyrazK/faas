package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const mcpTaskInboxWatchInterval = 30 * time.Second

type mcpTaskInboxEntry struct {
	mcpTaskStatusMetadata
	CreatedAt string `json:"createdAt"`
}

func cmdMCPTaskInbox(args []string) int {
	fs := newFlagSet("mcp-task-inbox", flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug; resolves its public URL")
	path := fs.String("endpoint", "/mcp", "endpoint path when using --app (default /mcp)")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP client token")
	watch := fs.Bool("watch", false, "follow new and changed inbox entries as JSON lines")
	waitTimeout := fs.Duration("wait-timeout", 0, "total time to wait for the first event (requires --watch --once)")
	var options mcpTaskInboxWatchOptions
	fs.BoolVar(&options.once, "once", false, "exit after printing one inbox event (requires --watch)")
	fs.DurationVar(&options.scanTimeout, "timeout", 30*time.Second, "deadline for one inbox read or watch scan")
	fs.DurationVar(&options.interval, "interval", mcpTaskInboxWatchInterval, "time between inbox scans when watching (1s–1h)")
	fs.StringVar(&options.checkpointPath, "checkpoint-file", "", "save inbox watch progress to a private file")
	var query mcphosting.TaskInboxQuery
	fs.StringVar(&query.Status, "status", "", "filter by retained Task status")
	fs.IntVar(&query.Limit, "limit", mcphosting.DefaultTaskInboxLimit, "maximum entries on this page (1–50)")
	fs.StringVar(&query.Cursor, "cursor", "", "nextCursor from the previous page")
	slug, args := peelLeadingSlug(args)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || options.scanTimeout <= 0 {
		return printErr("Invalid MCP Task inbox flags", errors.New("unexpected positional arguments or nonpositive timeout"))
	}
	if err := mergeLeadingSlug(app, slug); err != nil {
		return printErr("Invalid MCP Task inbox flags", err)
	}
	flagsSet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { flagsSet[f.Name] = true })
	if flagsSet["interval"] && !*watch {
		return printErr("Invalid MCP Task inbox flags", errors.New("--interval requires --watch"))
	}
	if flagsSet["checkpoint-file"] && (!*watch || options.checkpointPath == "") {
		return printErr("Invalid MCP Task inbox flags", errors.New("--checkpoint-file requires a path and --watch"))
	}
	if options.once && !*watch {
		return printErr("Invalid MCP Task inbox flags", errors.New("--once requires --watch"))
	}
	if flagsSet["wait-timeout"] && (!options.once || *waitTimeout <= 0) {
		return printErr("Invalid MCP Task inbox flags", errors.New("--wait-timeout requires a positive duration and --watch --once"))
	}
	if *watch && (query.Cursor != "" || options.interval < time.Second || options.interval > time.Hour) {
		return printErr("Invalid MCP Task inbox flags", errors.New("--watch starts at the first page and requires an --interval from 1s to 1h"))
	}
	if err := query.Validate(); err != nil {
		return printErr("Invalid MCP Task inbox flags", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	if token == "" {
		return printErr("MCP Task inbox", errors.New("provide a client credential with --token-env"))
	}
	parent := context.Background()
	if *watch {
		var stop context.CancelFunc
		parent, stop = signal.NotifyContext(parent, os.Interrupt)
		defer stop()
	}
	if *waitTimeout > 0 {
		var stop context.CancelFunc
		parent, stop = context.WithTimeout(parent, *waitTimeout)
		defer stop()
	}
	ctx, cancel := context.WithTimeout(parent, options.scanTimeout)
	defer cancel()
	resolved, err := mcpEndpoint(ctx, *endpoint, *app, *path)
	if err != nil {
		return printMCPTaskInboxError(parent, "MCP endpoint", err)
	}
	client, err := mcphosting.NewClient(resolved, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	client.HTTP.Timeout = options.scanTimeout
	if *watch {
		cancel()
		err := watchMCPTaskInbox(parent, client, query, options)
		if err == nil || (!options.once && parent.Err() != nil) {
			return 0
		}
		return printMCPTaskInboxError(parent, "MCP Task inbox watch", err)
	}
	page, _, err := client.ListTaskInbox(ctx, query)
	if err != nil {
		return printErr("MCP Task inbox", err)
	}
	entries := make([]mcpTaskInboxEntry, 0, len(page.Tasks))
	for _, task := range page.Tasks {
		entries = append(entries, mcpTaskInboxSnapshot(task))
	}
	return jsonOut(writeJSON(struct {
		Tasks      []mcpTaskInboxEntry `json:"tasks"`
		NextCursor string              `json:"nextCursor,omitempty"`
	}{Tasks: entries, NextCursor: page.NextCursor}))
}

func printMCPTaskInboxError(ctx context.Context, title string, err error) int {
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == context.DeadlineExceeded {
		return printErr("MCP Task inbox wait timed out", &exitErr{code: 124, msg: "--wait-timeout elapsed before an event was emitted"})
	}
	return printErr(title, err)
}

func mcpTaskInboxSnapshot(task mcphosting.Task) mcpTaskInboxEntry {
	return mcpTaskInboxEntry{mcpTaskSnapshot(task), task.CreatedAt}
}
