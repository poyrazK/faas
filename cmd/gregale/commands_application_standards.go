package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
)

type standardCLIOptions struct {
	org, standard, file, description, after string
	version, expected                       int64
	limit                                   int
}

func cmdOrgStandards(args []string) int {
	if code, handled := dispatchApplicationStandardCLI(args); handled {
		return code
	}
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale orgs standards <list|show|publish|assignments|application|local-intent|destinations|publishers|reviews|operation|exceptions> --org ORG [options]", "orgs")
		return 1
	}
	options, err := parseStandardCLI(args[0], args[1:])
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	var request api.CreateApplicationStandardVersionRequest
	if args[0] == "publish" {
		request, err = readStandardPublication(options)
		if err != nil {
			return printErr("Invalid application standard", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var result any
	switch args[0] {
	case "list":
		result, err = client.ListApplicationStandards(ctx, options.org, options.after, options.limit)
	case "show":
		result, err = client.GetApplicationStandardVersion(ctx, options.org, options.standard, options.version)
	case "publish":
		result, err = client.PublishApplicationStandardVersion(ctx, options.org, options.standard, request)
	}
	if err != nil {
		return printErr("Application standards operation failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardCLI(action string, args []string) (standardCLIOptions, error) {
	options := standardCLIOptions{expected: -1}
	fs := newFlagSet("orgs standards "+action, flag.ContinueOnError)
	fs.StringVar(&options.org, "org", "", "organization slug")
	switch action {
	case "list":
		fs.StringVar(&options.after, "after", "", "last standard slug from the previous page")
		fs.IntVar(&options.limit, "limit", api.ApplicationStandardMaxListPage, "page size")
	case "show":
		fs.StringVar(&options.standard, "standard", "", "standard slug")
		fs.Int64Var(&options.version, "version", 0, "immutable version; omitted reads the latest")
	case "publish":
		fs.StringVar(&options.standard, "standard", "", "standard slug")
		fs.StringVar(&options.file, "file", "", "standard definition JSON file")
		fs.StringVar(&options.description, "description", "", "version description")
		fs.Int64Var(&options.expected, "expected-version", -1, "current version; use 0 for a new standard")
	default:
		return options, fmt.Errorf("unknown operation %q", action)
	}
	if err := fs.Parse(args); err != nil {
		return options, err
	}
	if fs.NArg() != 0 || options.org == "" {
		return options, fmt.Errorf("--org is required and positional arguments are not accepted")
	}
	if action != "list" && options.standard == "" {
		return options, fmt.Errorf("--standard is required")
	}
	if action == "publish" && (options.file == "" || options.expected < 0) {
		return options, fmt.Errorf("--file and --expected-version are required")
	}
	if action == "list" && (options.limit < 1 || options.limit > api.ApplicationStandardMaxListPage) {
		return options, fmt.Errorf("limit must be between 1 and %d", api.ApplicationStandardMaxListPage)
	}
	if options.version < 0 {
		return options, fmt.Errorf("version cannot be negative")
	}
	return options, nil
}

func readStandardPublication(options standardCLIOptions) (api.CreateApplicationStandardVersionRequest, error) {
	file, err := openCustomerFile(options.file)
	if err != nil {
		return api.CreateApplicationStandardVersionRequest{}, fmt.Errorf("open definition: %w", err)
	}
	defer func() { _ = file.Close() }()
	definition, err := io.ReadAll(io.LimitReader(file, api.ApplicationStandardMaxDefinitionBytes+1))
	if err != nil {
		return api.CreateApplicationStandardVersionRequest{}, fmt.Errorf("read definition: %w", err)
	}
	if _, _, err := appstandards.Parse(definition, api.ApplicationStandardResolverLimits()); err != nil {
		return api.CreateApplicationStandardVersionRequest{}, err
	}
	return api.CreateApplicationStandardVersionRequest{Description: options.description, ExpectedVersion: options.expected, Definition: json.RawMessage(definition)}, nil
}
