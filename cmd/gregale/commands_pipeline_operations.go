package main

import (
	"context"
	"errors"
	"flag"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func cmdPipelineOperations(args []string) int {
	fs := newFlagSet("pipeline operations", flag.ContinueOnError)
	file := fs.String("file", "", "complete operations configuration JSON")
	revision := fs.Int64("revision", 0, "expected pipeline revision")
	snapshot := fs.Bool("run-snapshot", false, "request a snapshot during the next allowed work window")
	quality := fs.Bool("run-quality", false, "request quality checks during the next allowed work window")
	if parseInterspersed(fs, args) != nil {
		return 1
	}
	count := 0
	for _, selected := range []bool{*file != "", *snapshot, *quality} {
		if selected {
			count++
		}
	}
	if fs.NArg() != 1 || *revision < 1 || count != 1 {
		return printErr("Invalid operations request", errors.New("provide ID, --revision, and one of --file, --run-snapshot or --run-quality"))
	}
	id, err := canonicalPipelineID(fs.Arg(0))
	if err != nil {
		return printErr("Invalid pipeline ID", err)
	}
	r := api.DataPipelineOperationsRequest{ExpectedRevision: *revision}
	if *file != "" {
		r.Action = "configure"
		if err = readPipelineJSON(*file, &r.Config); err != nil || r.Config == nil {
			return printErr("Invalid operations file", errors.New("supply a non-null operations configuration"))
		}
	} else if *snapshot {
		r.Action = "run_snapshot"
	} else {
		r.Action = "run_quality"
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	p, err := client.ConfigureDataPipelineOperations(context.Background(), id, r)
	if err != nil {
		return printErr("Could not configure pipeline work", err)
	}
	return jsonOut(writeJSON(p))
}

// Existing managed mirrors make the first scheduled release independently
// reviewable; native setup/provisioning retains its existing CDC-only contract.
func createPipelineWithOperations(client *api.Client, r api.CreateDataPipelineRequest, wait bool, timeout time.Duration, apiName, authApp string) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	p, found, err := recoverPipelineCreate(ctx, client, r, r.DatabaseID, "")
	if err != nil {
		return printErr("Could not recover pipeline", err)
	}
	if found {
		return finishPipelineCreate(ctx, client, p, wait, apiName, authApp)
	}
	mode := ""
	if r.Operations.Snapshot != nil {
		mode = "snapshot"
	}
	check, err := requestConnectionCheck(ctx, client, r.SourceConnectionID, r.DatabaseID, api.RequestDataConnectionCheck{Mode: mode, Tables: r.Tables})
	if err == nil {
		check, err = waitConnectionCheck(ctx, client, check, time.Second)
	}
	if err == nil {
		err = datapipeline.ValidateServingSelection(check, r.Tables, r.Serving)
	}
	if err != nil {
		return printErr("Source and mirror check failed", err)
	}
	p, err = client.CreateDataPipeline(api.ContextWithIdempotencyKey(ctx, pipelineCreateKey(r)), r)
	if err != nil {
		return printErr("Could not create scheduled pipeline", err)
	}
	return finishPipelineCreate(ctx, client, p, wait, apiName, authApp)
}
