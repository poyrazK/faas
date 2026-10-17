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

type standardMutationCLIOptions struct {
	org, app, id, file string
}

func cmdOrgStandardMutation(action string, args []string) int {
	o, err := parseStandardMutationCLI(action, args)
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	var local api.SetApplicationStandardLocalIntentRequest
	var approve api.ApproveApplicationStandardExceptionRequest
	var revoke api.RevokeApplicationStandardExceptionRequest
	target := map[string]any{"local-intent": &local, "approve": &approve, "revoke": &revoke}[action]
	if err := readStandardMutationCLI(o.file, target); err != nil {
		return printErr("Invalid application standards mutation", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result any
	ctx := context.Background()
	switch action {
	case "local-intent":
		result, err = client.SetApplicationStandardLocalIntent(ctx, o.org, o.app, local)
	case "approve":
		result, err = client.ApproveApplicationStandardException(ctx, o.org, o.app, approve)
	case "revoke":
		result, err = client.RevokeApplicationStandardException(ctx, o.org, o.app, o.id, revoke)
	}
	if err != nil {
		return printErr("Application standards mutation failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardMutationCLI(action string, args []string) (standardMutationCLIOptions, error) {
	var o standardMutationCLIOptions
	if action != "local-intent" && action != "approve" && action != "revoke" {
		return o, fmt.Errorf("unknown application standards mutation %q", action)
	}
	fs := newFlagSet("orgs standards "+action, flag.ContinueOnError)
	fs.StringVar(&o.org, "org", "", "organization slug")
	fs.StringVar(&o.app, "app", "", "application UUID")
	fs.StringVar(&o.file, "file", "", "complete mutation JSON with expected_revision")
	if action == "revoke" {
		fs.StringVar(&o.id, "id", "", "exception UUID")
	}
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	app, err := uuid.Parse(o.app)
	if o.org == "" || o.file == "" || err != nil || app == uuid.Nil || fs.NArg() != 0 {
		return o, fmt.Errorf("--org, --file and a nonzero --app UUID are required; positional arguments are not accepted")
	}
	o.app = app.String()
	if action == "revoke" {
		id, err := uuid.Parse(o.id)
		if err != nil || id == uuid.Nil {
			return o, fmt.Errorf("--id must be a nonzero exception UUID")
		}
		o.id = id.String()
	}
	return o, nil
}

func readStandardMutationCLI(path string, target any) error {
	file, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, api.ApplicationStandardMaxDefinitionBytes+1))
	if err != nil {
		return err
	}
	if len(raw) > api.ApplicationStandardMaxDefinitionBytes {
		return fmt.Errorf("application standards mutation request is too large")
	}
	return json.Unmarshal(raw, target)
}
