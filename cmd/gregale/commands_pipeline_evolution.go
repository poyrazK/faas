package main

import (
	"context"
	"errors"
	"flag"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPipelineEvolve(args []string) int {
	fs := newFlagSet("pipeline evolve", flag.ContinueOnError)
	file := fs.String("file", "", "JSON preview request (kind plus columns, auth_organizations, or relationships)")
	approve := fs.String("approve", "", "approve the saved change ID")
	discard := fs.String("discard", "", "discard the saved preview ID")
	retry := fs.String("retry", "", "retry the failed API refresh for this approved change ID")
	schema := fs.String("approve-schema", "", "approve the worker's exact source schema review ID")
	retrySchema := fs.Bool("retry-schema", false, "retry an approved schema workflow; requires --approve-schema")
	revision := fs.Int64("revision", 0, "expected pipeline revision")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	if fs.NArg() != 1 || *revision < 1 {
		return printErr("Invalid evolution request", errors.New("provide a pipeline ID, --revision, and exactly one of --file, --approve or --discard"))
	}
	id, idErr := canonicalPipelineID(fs.Arg(0))
	if idErr != nil {
		return printErr("Invalid pipeline ID", idErr)
	}
	count := 0
	for _, s := range []string{*file, *approve, *discard, *retry, *schema} {
		if s != "" {
			count++
		}
	}
	if count != 1 || *retrySchema && *schema == "" {
		return printErr("Invalid evolution request", errors.New("choose one action"))
	}
	r := api.DataPipelineEvolutionRequest{ExpectedRevision: *revision}
	if *file != "" {
		var input *api.DataPipelineEvolutionRequest
		if err := readPipelineJSON(*file, &input); err != nil {
			return printErr("Invalid evolution JSON", err)
		}
		if input == nil || input.Action != "" || input.ChangeID != "" || input.ExpectedRevision != 0 {
			return printErr("Invalid evolution JSON", errors.New("the file must contain preview fields only; --revision supplies the revision"))
		}
		r = *input
		r.ExpectedRevision = *revision
		r.Action = "preview"
	} else if *approve != "" {
		r.Action, r.ChangeID = "approve", *approve
	} else if *retry != "" {
		r.Action, r.ChangeID = "retry", *retry
	} else {
		r.Action, r.ChangeID = "discard", *discard
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *schema != "" {
		p, err := client.ApproveDataPipelineSchema(context.Background(), id, api.DataPipelineSchemaApprovalRequest{ExpectedRevision: *revision, ChangeID: *schema, Retry: *retrySchema})
		if err != nil {
			return printErr("Could not approve exact source schema", err)
		}
		return jsonOut(writeJSON(p))
	}
	p, err := client.EvolveDataPipeline(context.Background(), id, r)
	if err != nil {
		return printErr("Could not save pipeline evolution", err)
	}
	return jsonOut(writeJSON(p))
}
