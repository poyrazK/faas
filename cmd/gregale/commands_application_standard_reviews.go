package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type standardInspectionCLIOptions struct {
	org, id, file, app, after string
	limit                     int
}

func cmdOrgStandardInspection(kind string, args []string) int {
	action, args, err := standardInspectionAction(kind, args)
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	options, err := parseStandardInspectionCLI(action, args)
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	var request api.ApplicationStandardReviewRequest
	if action == "preview" {
		request, err = readStandardReviewCLI(options.file)
		if err != nil {
			return printErr("Invalid application standard review", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result any
	ctx := context.Background()
	switch action {
	case "preview":
		result, err = client.PreviewApplicationStandardAssignment(ctx, options.org, request)
	case "show":
		result, err = client.GetApplicationStandardReview(ctx, options.org, options.id)
	case "operation":
		result, err = client.GetApplicationStandardOperation(ctx, options.org, options.id)
	case "exceptions":
		result, err = client.ListApplicationStandardExceptions(ctx, options.org, options.app, options.after, options.limit)
	}
	if err != nil {
		return printErr("Application standards inspection failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardInspectionCLI(action string, args []string) (standardInspectionCLIOptions, error) {
	var o standardInspectionCLIOptions
	name := "orgs standards "
	if action == "preview" || action == "show" {
		name += "reviews "
	}
	fs := newFlagSet(name+action, flag.ContinueOnError)
	fs.StringVar(&o.org, "org", "", "organization slug")
	switch action {
	case "preview":
		fs.StringVar(&o.file, "file", "", "assignment review JSON file")
	case "show", "operation":
		fs.StringVar(&o.id, "id", "", "review or operation UUID")
	case "exceptions":
		fs.StringVar(&o.app, "app", "", "application UUID")
		fs.StringVar(&o.after, "after", "", "last exception UUID from the previous page")
		fs.IntVar(&o.limit, "limit", api.ApplicationStandardMaxListPage, "page size")
	default:
		return o, fmt.Errorf("unknown inspection operation %q", action)
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.org == "" || fs.NArg() != 0 {
		return o, fmt.Errorf("--org is required; positional arguments are not accepted")
	}
	if action == "preview" && o.file == "" {
		return o, fmt.Errorf("--file is required")
	}
	if action == "show" || action == "operation" {
		id, err := uuid.Parse(o.id)
		if err != nil || id == uuid.Nil {
			return o, fmt.Errorf("--id must be a nonzero UUID")
		}
		o.id = id.String()
	}
	if action == "exceptions" {
		return validateStandardExceptionCLI(o)
	}
	return o, nil
}

func validateStandardExceptionCLI(o standardInspectionCLIOptions) (standardInspectionCLIOptions, error) {
	id, err := uuid.Parse(o.app)
	if err != nil || id == uuid.Nil {
		return o, fmt.Errorf("--app must be a nonzero UUID")
	}
	o.app = id.String()
	if o.after != "" {
		id, err = uuid.Parse(o.after)
		if err != nil || id == uuid.Nil {
			return o, fmt.Errorf("--after must be a nonzero UUID")
		}
		o.after = id.String()
	}
	if o.limit < 1 || o.limit > api.ApplicationStandardMaxListPage {
		return o, fmt.Errorf("limit is outside the application standard page bounds")
	}
	return o, nil
}

func readStandardReviewCLI(path string) (api.ApplicationStandardReviewRequest, error) {
	var result api.ApplicationStandardReviewRequest
	file, err := openCustomerFile(path)
	if err != nil {
		return result, err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.ApplicationStandardMaxDefinitionBytes+1))
	if err != nil {
		return result, err
	}
	if len(raw) > api.ApplicationStandardMaxDefinitionBytes {
		return result, fmt.Errorf("review request is too large")
	}
	err = json.Unmarshal(raw, &result)
	return result, err
}

func standardInspectionAction(kind string, args []string) (string, []string, error) {
	if kind != "reviews" {
		return kind, args, nil
	}
	if len(args) == 0 || args[0] != "preview" && args[0] != "show" {
		return "", nil, fmt.Errorf("reviews requires preview or show")
	}
	return args[0], args[1:], nil
}
