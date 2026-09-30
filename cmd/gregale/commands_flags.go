package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdFlags(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale flags <get|apply|history|inspect|rollback|requests> --project SLUG [flags]", "flags")
		return 1
	}
	fs := newFlagSet("flags-"+args[0], flag.ContinueOnError)
	project := fs.String("project", "", "project slug")
	environment := fs.String("environment", "production", "named project environment")
	file := fs.String("file", "", "JSON update with expected_version and config")
	key := fs.String("key", "", "flag key")
	customer := fs.String("customer-id", "", "verified platform customer UUID")
	version := fs.Int64("version", 0, "historical version for inspect or rollback")
	expected := fs.Int64("expected-version", -1, "current version required for rollback")
	before := fs.Int64("before-version", 0, "history pagination boundary")
	value := fs.String("value", "", "filter requests by true or false")
	used := fs.String("used", "", "filter requests by true or false exposure")
	since := fs.String("since", "24h", "request evidence lookback")
	cursor := fs.String("cursor", "", "request evidence next-page cursor")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *project == "" || *environment == "" {
		PrintUsage(os.Stderr, "--project is required", "flags")
		return 1
	}
	valid := false
	switch args[0] {
	case "get", "history":
		valid = true
	case "apply":
		valid = *file != ""
	case "inspect", "requests":
		valid = *key != ""
	case "rollback":
		valid = *version > 0 && *expected >= 0
	}
	if !valid {
		PrintUsage(os.Stderr, "invalid flags command or missing required options", "flags")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var out json.RawMessage
	switch args[0] {
	case "get":
		out, err = client.ProjectFlags(ctx, *project, *environment)
	case "history":
		out, err = client.ProjectFlagVersions(ctx, *project, *environment, *before)
	case "apply":
		var raw json.RawMessage
		raw, err = readFlagsUpdate(*file)
		if err == nil {
			out, err = client.PublishProjectFlags(ctx, *project, *environment, raw)
		}
	case "inspect":
		out, err = client.InspectProjectFlag(ctx, *project, *environment, *key, *customer, *version)
	case "rollback":
		out, err = client.RollbackProjectFlags(ctx, *project, *environment, *expected, *version)
	case "requests":
		out, err = client.ProjectFlagRequests(ctx, *project, *environment, *key, url.Values{"customer_id": {*customer}, "value": {*value}, "used": {*used}, "since": {*since}, "cursor": {*cursor}})
	}
	if err != nil {
		return printErr("Flags operation failed", err)
	}
	return jsonOut(writeJSON(out))
}

func readFlagsUpdate(path string) (json.RawMessage, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, fmt.Errorf("open flag update: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.FlagsMaxBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read flag update: %w", err)
	}
	if len(raw) > api.FlagsMaxBundleBytes {
		return nil, fmt.Errorf("flag update exceeds %d bytes", api.FlagsMaxBundleBytes)
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid flag update JSON")
	}
	return raw, nil
}
