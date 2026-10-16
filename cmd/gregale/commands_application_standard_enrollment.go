package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/uuid"
)

func cmdOrgStandardApplication(args []string) int {
	org, appID, err := parseStandardApplicationCLI(args)
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	result, err := client.GetApplicationStandardEnrollment(context.Background(), org, appID)
	if err != nil {
		return printErr("Application standards read failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardApplicationCLI(args []string) (string, string, error) {
	var org, appID string
	fs := newFlagSet("orgs standards application", flag.ContinueOnError)
	fs.StringVar(&org, "org", "", "organization slug")
	fs.StringVar(&appID, "app", "", "application UUID")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	id, err := uuid.Parse(appID)
	if org == "" || err != nil || id == uuid.Nil || fs.NArg() != 0 {
		return "", "", fmt.Errorf("--org and a valid --app UUID are required; positional arguments are not accepted")
	}
	return org, id.String(), nil
}
