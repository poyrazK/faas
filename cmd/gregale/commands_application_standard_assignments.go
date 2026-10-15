package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdOrgStandardAssignments(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid application standards command", fmt.Errorf("assignments requires list or show"))
	}
	o, err := parseStandardAssignmentCLI(args[0], args[1:])
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result any
	if args[0] == "show" {
		result, err = client.GetApplicationStandardAssignment(context.Background(), o.org, o.id)
	} else {
		result, err = client.ListApplicationStandardAssignments(context.Background(), o.org, o.after, o.limit)
	}
	if err != nil {
		return printErr("Application standards assignment read failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardAssignmentCLI(action string, args []string) (standardResourceCLIOptions, error) {
	var o standardResourceCLIOptions
	fs := newFlagSet("orgs standards assignments "+action, flag.ContinueOnError)
	fs.StringVar(&o.org, "org", "", "organization slug")
	switch action {
	case "list":
		fs.StringVar(&o.after, "after", "", "last assignment UUID from the previous page")
		fs.IntVar(&o.limit, "limit", api.ApplicationStandardMaxListPage, "page size")
	case "show":
		fs.StringVar(&o.id, "id", "", "assignment UUID")
	default:
		return o, fmt.Errorf("assignments requires list or show")
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.org == "" || fs.NArg() != 0 {
		return o, fmt.Errorf("--org is required; positional arguments are not accepted")
	}
	if action == "show" || o.after != "" {
		value := o.after
		if action == "show" {
			value = o.id
		}
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil {
			return o, fmt.Errorf("assignment ID and page cursor must be nonzero UUIDs")
		}
		if action == "show" {
			o.id = id.String()
		} else {
			o.after = id.String()
		}
	}
	if action == "list" && (o.limit < 1 || o.limit > api.ApplicationStandardMaxListPage) {
		return o, fmt.Errorf("limit is outside the application standard page bounds")
	}
	return o, nil
}
