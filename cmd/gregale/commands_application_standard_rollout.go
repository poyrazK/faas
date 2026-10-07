package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdOrgStandardRollout(action string, args []string) int {
	o, err := parseStandardRolloutCLI(action, args)
	if err != nil {
		return printErr("Invalid application standards command", err)
	}
	var approve api.ApproveApplicationStandardReviewRequest
	var control api.ControlApplicationStandardOperationRequest
	var target any = &control
	if action == "approve" {
		target = &approve
	}
	if err := readStandardMutationCLI(o.file, target); err != nil {
		return printErr("Invalid application standards rollout request", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var result api.ApplicationStandardOperation
	ctx := context.Background()
	switch action {
	case "approve":
		result, err = client.ApproveApplicationStandardReview(ctx, o.org, o.id, approve)
	case "pause":
		result, err = client.PauseApplicationStandardOperation(ctx, o.org, o.id, control)
	case "resume":
		result, err = client.ResumeApplicationStandardOperation(ctx, o.org, o.id, control)
	case "abort":
		result, err = client.AbortApplicationStandardOperation(ctx, o.org, o.id, control)
	}
	if err != nil {
		return printErr("Application standards rollout mutation failed", err)
	}
	return jsonOut(writeJSON(result))
}

func parseStandardRolloutCLI(action string, args []string) (standardInspectionCLIOptions, error) {
	var o standardInspectionCLIOptions
	if action != "approve" && action != "pause" && action != "resume" && action != "abort" {
		return o, fmt.Errorf("unknown rollout action %q", action)
	}
	fs := newFlagSet("orgs standards "+action, flag.ContinueOnError)
	fs.StringVar(&o.org, "org", "", "organization slug")
	fs.StringVar(&o.id, "id", "", "review or operation UUID")
	fs.StringVar(&o.file, "file", "", "approval_hash or expected_updated_at JSON file")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	id, err := uuid.Parse(o.id)
	if o.org == "" || o.file == "" || err != nil || id == uuid.Nil || fs.NArg() != 0 {
		return o, fmt.Errorf("--org, --file and a nonzero --id UUID are required; positional arguments are not accepted")
	}
	o.id = id.String()
	return o, nil
}

func dispatchApplicationStandardCLI(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	if len(args) > 1 && args[0] == "reviews" && args[1] == "approve" {
		return cmdOrgStandardRollout("approve", args[2:]), true
	}
	if len(args) > 1 && args[0] == "operation" && (args[1] == "pause" || args[1] == "resume" || args[1] == "abort") {
		return cmdOrgStandardRollout(args[1], args[2:]), true
	}
	if args[0] == "local-intent" {
		return cmdOrgStandardMutation("local-intent", args[1:]), true
	}
	if len(args) > 1 && args[0] == "exceptions" && (args[1] == "approve" || args[1] == "revoke") {
		return cmdOrgStandardMutation(args[1], args[2:]), true
	}
	switch args[0] {
	case "reviews", "operation", "exceptions":
		return cmdOrgStandardInspection(args[0], args[1:]), true
	case "assignments":
		return cmdOrgStandardAssignments(args[1:]), true
	case "application":
		return cmdOrgStandardApplication(args[1:]), true
	case "destinations", "publishers":
		return cmdOrgStandardResources(args[0], args[1:]), true
	}
	return 0, false
}
