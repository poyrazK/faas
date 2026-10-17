package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func cmdBindingVerificationRunList(args []string, graph bool) int {
	flags, pos := splitArgsForFlags(args, "include-members")
	fs := newFlagSet("verification-runs-list", flag.ContinueOnError)
	limit := fs.Int("limit", api.BindingVerificationRunListDefaultLimit, "page size (1–100)")
	cursor := fs.String("cursor", "", "next cursor from a previous page; reuse filters")
	deployment := fs.String("deployment", "", "exact deployment UUID filter")
	scope := fs.String("scope", "", "app deployment scope filter")
	status := fs.String("status", "", "retained state: pending, passed, cancelled, expired")
	members := fs.Bool("include-members", false, "include graph member receipts in app listings")
	if e := fs.Parse(flags); e != nil || fs.NArg() != 0 || *limit < 1 || *limit > api.BindingVerificationRunListMaxLimit || !graph && len(pos) != 1 || graph && len(pos) != 2 {
		return 1
	}
	if !graph && !api.ValidAppSlug(pos[0]) || graph && (!api.ValidProjectSlug(pos[0]) || !api.ValidProjectEnvironmentSlug(pos[1])) {
		return 1
	}
	invalid := false
	fs.Visit(func(f *flag.Flag) { invalid = invalid || graph && (f.Name == "scope" || f.Name == "include-members") })
	if invalid {
		return 1
	}
	if *deployment != "" {
		id, e := uuid.Parse(*deployment)
		if e != nil || id == uuid.Nil {
			return 1
		}
		*deployment = id.String()
	}
	client, e := authedClient()
	if e != nil {
		return printErr("Not logged in", e)
	}
	o := api.BindingVerificationRunListOptions{Limit: *limit, Cursor: *cursor, DeploymentID: *deployment, Scope: *scope, Status: *status, IncludeMembers: *members}
	var result api.BindingVerificationRunList
	if graph {
		result, e = client.ListProjectBindingVerificationRuns(context.Background(), pos[0], pos[1], o)
	} else {
		result, e = client.ListBindingVerificationRuns(context.Background(), pos[0], o)
	}
	if e != nil {
		return printErr("Could not list verification receipts", e)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintln(osStdout, "ID\tSTATE\tCREATED\tEXPIRES\tGRAPH_RUN_ID")
	for _, r := range result.Runs {
		_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Status, r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), r.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"), r.GraphRunID)
	}
	if result.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "Next cursor: %s\n", result.NextCursor)
	}
	return 0
}
