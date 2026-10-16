package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

type standardResourceCLIOptions struct {
	org, id, file, after string
	limit                int
}

func cmdOrgStandardResources(kind string, args []string) int {
	if len(args) == 0 {
		return printErr("Invalid standard resource command", fmt.Errorf("use list, show or create with --org"))
	}
	options, err := parseStandardResourceCLI(kind, args[0], args[1:])
	if err != nil {
		return printErr("Invalid standard resource command", err)
	}
	var destination api.CreateApplicationStandardLogDestinationRequest
	var publisher api.CreateApplicationStandardPublisherRequest
	if args[0] == "create" {
		request := any(&destination)
		if kind == "publishers" {
			request = &publisher
		}
		if err := readStandardResourceRequest(options.file, request); err != nil {
			return printErr("Invalid resource file", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var result any
	switch kind + "/" + args[0] {
	case "destinations/create":
		result, err = client.CreateApplicationStandardLogDestination(ctx, options.org, destination)
	case "destinations/show":
		result, err = client.GetApplicationStandardLogDestination(ctx, options.org, options.id)
	case "destinations/list":
		result, err = client.ListApplicationStandardLogDestinations(ctx, options.org, options.after, options.limit)
	case "publishers/create":
		result, err = client.CreateApplicationStandardPublisher(ctx, options.org, publisher)
	case "publishers/show":
		result, err = client.GetApplicationStandardPublisher(ctx, options.org, options.id)
	case "publishers/list":
		result, err = client.ListApplicationStandardPublishers(ctx, options.org, options.after, options.limit)
	}
	if err != nil {
		return printErr("Standard resource operation failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardResourceCLI(kind, action string, args []string) (standardResourceCLIOptions, error) {
	options := standardResourceCLIOptions{}
	fs := newFlagSet("orgs standards "+kind+" "+action, flag.ContinueOnError)
	fs.StringVar(&options.org, "org", "", "organization slug")
	switch action {
	case "list":
		fs.StringVar(&options.after, "after", "", "last resource UUID from the previous page")
		fs.IntVar(&options.limit, "limit", api.ApplicationStandardMaxListPage, "page size")
	case "show":
		fs.StringVar(&options.id, "id", "", "resource UUID")
	case "create":
		fs.StringVar(&options.file, "file", "", "resource JSON file; credentials are sealed server-side")
	default:
		return options, fmt.Errorf("unknown operation %q", action)
	}
	if err := fs.Parse(args); err != nil {
		return options, err
	}
	if fs.NArg() != 0 || options.org == "" {
		return options, fmt.Errorf("--org is required and positional arguments are not accepted")
	}
	if action == "show" && options.id == "" {
		return options, fmt.Errorf("--id is required")
	}
	if action == "create" && options.file == "" {
		return options, fmt.Errorf("--file is required")
	}
	if action == "list" && (options.limit < 1 || options.limit > api.ApplicationStandardMaxListPage) {
		return options, fmt.Errorf("limit must be between 1 and %d", api.ApplicationStandardMaxListPage)
	}
	return options, nil
}

func readStandardResourceRequest(path string, target any) error {
	file, err := openCustomerFile(path)
	if err != nil {
		return fmt.Errorf("open resource file: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.ApplicationStandardMaxDefinitionBytes+1))
	if err != nil {
		return fmt.Errorf("could not read resource file")
	}
	if len(raw) > api.ApplicationStandardMaxDefinitionBytes {
		return fmt.Errorf("resource file is too large")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("resource file must contain one valid JSON object with supported fields")
	}
	return nil
}
