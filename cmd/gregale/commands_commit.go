package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
)

func cmdCommit(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale commit <add|connection|pause|resume|info|operation|receipt|blocked|replay>", "commit")
		return 1
	}
	flags, positional := splitArgsForFlags(args[1:])
	fs := newFlagSet("commit "+args[0], flag.ContinueOnError)
	name := fs.String("name", "", "unique account source name")
	operationPolicy := fs.String("operation-policy", "", "account-scoped queue policy for managed Operations")
	file := fs.String("file", "", "file containing PostgreSQL connection URL (never print it)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	verb := args[0]
	if verb != "add" && *operationPolicy != "" {
		return 1
	}
	switch verb {
	case "add":
		if len(positional) != 1 || strings.TrimSpace(*name) == "" || strings.TrimSpace(*operationPolicy) == "" || *file != "" {
			return 1
		}
	case "connection":
		if len(positional) != 1 || *file == "" || *name != "" {
			return 1
		}
	case "pause", "resume", "info", "operation", "blocked":
		if len(positional) != 1 || *file != "" || *name != "" {
			return 1
		}
	case "receipt", "replay":
		if len(positional) != 2 || *file != "" || *name != "" {
			return 1
		}
	default:
		PrintUsage(os.Stderr, "unknown commit command", "commit")
		return 1
	}
	if verb != "add" {
		for _, id := range positional {
			if _, err := uuid.Parse(id); err != nil {
				return printErr("Invalid Commit identity", err)
			}
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var result any
	switch verb {
	case "add":
		result, err = client.CreateCommitSource(ctx, positional[0], *name, *operationPolicy)
	case "pause", "resume":
		result, err = client.SetCommitSourceEnabled(ctx, positional[0], verb == "resume")
	case "blocked":
		result, err = client.ListCommitBlockedEvents(ctx, positional[0])
	case "replay":
		err = client.ReplayCommitBlockedEvent(ctx, positional[0], positional[1])
		if err == nil {
			PrintOK(osStdout, "Commit replay requested.")
			return 0
		}
	case "info":
		result, err = client.GetCommitSource(ctx, positional[0])
	case "operation":
		result, err = client.GetCommitOperation(ctx, positional[0])
	case "receipt":
		result, err = client.GetCommitReceipt(ctx, positional[0], positional[1])
	case "connection":
		connectionFile, readErr := openCustomerFile(*file)
		if readErr != nil {
			return printErr("Cannot read connection file", readErr)
		}
		defer func() { _ = connectionFile.Close() }()
		contents, readErr := io.ReadAll(io.LimitReader(connectionFile, 8193))
		if readErr != nil {
			return printErr("Cannot read connection file", readErr)
		}
		if len(contents) > 8192 {
			return printErr("Connection file exceeds 8192 bytes", fmt.Errorf("file too large"))
		}
		err = client.PutCommitSourceConnection(ctx, positional[0], strings.TrimSpace(string(contents)))
		if err == nil {
			PrintOK(osStdout, "Database connection sealed.")
			return 0
		}
	}
	if err != nil {
		return printErr("Commit request failed", err)
	}
	return jsonOut(writeJSON(result))
}
