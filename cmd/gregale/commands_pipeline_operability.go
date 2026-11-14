package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdPipelineSetup(args []string) int {
	if len(args) == 0 {
		return printErr("Missing setup command", errors.New("use plan, create, list, status, wait, retry or cancel"))
	}
	if args[0] == "create" || args[0] == "plan" {
		flags := append([]string{"--durable"}, args[1:]...)
		if args[0] == "plan" {
			flags = append(flags, "--plan")
		}
		return cmdPipelineCreate(flags)
	}
	fs := newFlagSet("pipeline setup "+args[0], flag.ContinueOnError)
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum wait, up to 1h")
	revision := fs.Int64("revision", 0, "expected revision; defaults to the current revision")
	yes := fs.Bool("yes", false, "confirm cancellation; allocated resources are retained")
	if err := parseInterspersed(fs, args[1:]); err != nil {
		return 1
	}
	action := args[0]
	if action != "list" && action != "status" && action != "wait" && action != "retry" && action != "cancel" || action == "list" && fs.NArg() != 0 || action != "list" && fs.NArg() != 1 || *timeout <= 0 || *timeout > time.Hour || *revision < 0 || action == "cancel" && !*yes {
		return printErr("Invalid setup command", errors.New("supply a saved setup ID; cancel requires --yes; timeout must be at most 1h"))
	}
	id := ""
	var err error
	if action != "list" {
		id, err = canonicalPipelineID(fs.Arg(0))
		if err != nil {
			return printErr("Invalid setup ID", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if action == "list" {
		out, err := client.ListDataPipelineSetups(ctx)
		if err != nil {
			return printErr("Could not list saved setups", err)
		}
		return jsonOut(writeJSON(out))
	}
	job, err := client.GetDataPipelineSetup(ctx, id)
	if err != nil {
		return printErr("Could not inspect saved setup", err)
	}
	if action == "retry" || action == "cancel" {
		if *revision == 0 {
			*revision = job.Revision
		}
		job, err = client.ControlDataPipelineSetup(ctx, id, api.DataPipelineSetupControlRequest{ExpectedRevision: *revision, Action: action})
		if err != nil {
			return printErr("Could not update saved setup", err)
		}
	}
	if action == "wait" {
		job, err = waitPipelineSetup(ctx, client, job)
	}
	if outErr := writeJSON(job); outErr != nil {
		return jsonOut(outErr)
	}
	if err != nil {
		return printErr("Setup is saved; inspect pipeline setup status "+id, err)
	}
	return 0
}

func durablePipelineCreate(client *api.Client, req api.DataPipelineSetupRequest, plan, wait bool, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if req.DataAPIName != "" {
		app, err := client.GetApp(ctx, req.AuthAppID)
		if err != nil {
			return printErr("Could not resolve Auth app before saving setup", err)
		}
		req.AuthAppID = app.ID
	}
	raw, _ := json.Marshal(req)
	keyed := api.ContextWithIdempotencyKey(ctx, addResourceIdempotencyKey("pipeline-setup", string(raw)))
	if plan {
		out, err := client.PlanDataPipelineSetup(ctx, req)
		if pipelineCheckRequired(err) {
			if checkErr := discoverDurableSetup(ctx, client, req); checkErr != nil {
				return printErr("Source discovery failed; no mirror was created", checkErr)
			}
			out, err = client.PlanDataPipelineSetup(ctx, req)
		}
		if err != nil {
			return printErr("Could not preview setup resources", err)
		}
		return jsonOut(writeJSON(out))
	}
	job, err := client.CreateDataPipelineSetup(keyed, req)
	if pipelineCheckRequired(err) {
		if checkErr := discoverDurableSetup(ctx, client, req); checkErr != nil {
			return printErr("Source discovery failed; no mirror was created", checkErr)
		}
		job, err = client.CreateDataPipelineSetup(keyed, req)
	}
	if err != nil {
		return printErr("Could not confirm saved setup; retry the same immutable request or inspect pipeline setup list", err)
	}
	if !wait {
		return jsonOut(writeJSON(job))
	}
	// Keep stdout as one machine-readable receipt while exposing the recovery ID
	// before a possibly long wait in the terminal's progress stream.
	PrintProgress(osStderr, "Saved setup %s; resume with gregale pipeline setup wait %s", job.ID, job.ID)
	job, err = waitPipelineSetup(ctx, client, job)
	if outErr := writeJSON(job); outErr != nil {
		return jsonOut(outErr)
	}
	if err != nil {
		return printErr("Resources are retained; resume with pipeline setup wait "+job.ID, err)
	}
	return 0
}
func pipelineCheckRequired(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Code == "data_connection_check_required"
}
func discoverDurableSetup(ctx context.Context, client *api.Client, r api.DataPipelineSetupRequest) error {
	check, err := requestConnectionCheck(ctx, client, r.SourceConnectionID, "", api.RequestDataConnectionCheck{Tables: r.Tables})
	if err != nil {
		return err
	}
	check, err = waitConnectionCheck(ctx, client, check, time.Second)
	// A capacity/provider blocker may still leave usable discovery metadata.
	// The durable worker records the blocker and rechecks it before provisioning.
	if err != nil && len(check.Tables) == 0 {
		return err
	}
	return nil
}
func waitPipelineSetup(ctx context.Context, client *api.Client, job api.DataPipelineSetup) (api.DataPipelineSetup, error) {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		switch job.State {
		case "ready":
			return job, nil
		case "blocked", "cancelled":
			return job, fmt.Errorf("%s: %s", job.State, job.Recovery)
		}
		select {
		case <-ctx.Done():
			return job, ctx.Err()
		case <-timer.C:
			next, err := client.GetDataPipelineSetup(ctx, job.ID)
			if err != nil {
				return job, err
			}
			job = next
		}
	}
}

func cmdPipelineFreshness(args []string) int {
	fs := newFlagSet("pipeline freshness", flag.ContinueOnError)
	target := fs.Duration("target", 0, "checkpoint confirmation target, 15s to 24h; 0 disables it")
	revision := fs.Int64("revision", 0, "expected revision; defaults to current")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	specified := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "target" {
			specified = true
		}
	})
	if fs.NArg() != 1 || *revision < 0 || specified && (!validPipelineFreshnessDuration(*target)) {
		return printErr("Invalid freshness target", errors.New("supply an ID and --target 0 or a whole-second duration from 15s to 24h"))
	}
	id, err := canonicalPipelineID(fs.Arg(0))
	if err != nil {
		return printErr("Invalid pipeline ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	p, err := client.GetDataPipeline(context.Background(), id)
	if err != nil {
		return printErr("Could not inspect pipeline", err)
	}
	if specified {
		if *revision == 0 {
			*revision = p.Revision
		}
		p, err = client.ConfigureDataPipelineFreshness(context.Background(), id, api.DataPipelineFreshnessRequest{ExpectedRevision: *revision, TargetSeconds: int64(*target / time.Second)})
		if err != nil {
			return printErr("Could not save freshness target", err)
		}
	}
	return jsonOut(writeJSON(p))
}
func cmdPipelineIndexes(args []string) int {
	fs := newFlagSet("pipeline indexes", flag.ContinueOnError)
	table := fs.String("table", "", "selected table to inspect")
	filters := fs.String("filters", "", "comma-separated equality filter column names; no values")
	order := fs.String("order-by", "", "comma-separated ordered columns; prefix descending columns with -")
	budget := fs.Int64("max-index-bytes", 0, "approved storage budget; default and maximum 32 MiB")
	approve := fs.String("approve", "", "approve exactly this frozen advice ID")
	discard := fs.Bool("discard", false, "discard unapproved advice")
	revision := fs.Int64("revision", 0, "expected revision; defaults to current")
	wait := fs.Bool("wait", false, "wait for advice or the approved build")
	timeout := fs.Duration("timeout", 2*time.Minute, "maximum wait, up to 1h")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	modes := 0
	for _, b := range []bool{*table != "", *approve != "", *discard} {
		if b {
			modes++
		}
	}
	if fs.NArg() != 1 || modes > 1 || *revision < 0 || *timeout <= 0 || *timeout > time.Hour || *budget < 0 || *budget > api.DataPipelineIndexBuildMaxBytes || *table == "" && (*filters != "" || *order != "" || *budget != 0) {
		return printErr("Invalid index request", errors.New("supply an ID and one of --table, --approve, or --discard; omit actions to inspect advice"))
	}
	id, err := canonicalPipelineID(fs.Arg(0))
	if err != nil {
		return printErr("Invalid pipeline ID", err)
	}
	req := api.DataPipelineIndexRequest{Table: *table, AdviceID: *approve, MaxIndexBytes: *budget}
	if *table != "" {
		req.Action = "preview"
	}
	if *approve != "" {
		req.Action = "approve"
	}
	if *discard {
		req.Action = "discard"
	}
	if *filters != "" {
		req.Filters = strings.Split(*filters, ",")
	}
	if *order != "" {
		for _, name := range strings.Split(*order, ",") {
			req.OrderBy = append(req.OrderBy, api.DataPipelineIndexOrder{Column: strings.TrimPrefix(name, "-"), Descending: strings.HasPrefix(name, "-")})
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	p, err := client.GetDataPipeline(ctx, id)
	if err != nil {
		return printErr("Could not inspect pipeline", err)
	}
	if req.Action != "" {
		req.ExpectedRevision = *revision
		if req.ExpectedRevision == 0 {
			req.ExpectedRevision = p.Revision
		}
		p, err = client.ChangeDataPipelineIndexes(ctx, id, req)
		if err != nil {
			return printErr("Could not update index advice", err)
		}
	}
	if *wait {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for p.IndexAdvice != nil && (p.IndexAdvice.State == "requested" || p.IndexAdvice.State == "approved") {
			select {
			case <-ctx.Done():
				_ = writeJSON(p)
				return printErr("Index request is saved; inspect pipeline indexes "+id, ctx.Err())
			case <-timer.C:
				p, err = client.GetDataPipeline(ctx, id)
				if err != nil {
					return printErr("Could not inspect saved index request", err)
				}
			}
		}
	}
	if outErr := writeJSON(p); outErr != nil {
		return jsonOut(outErr)
	}
	if p.IndexAdvice != nil && p.IndexAdvice.State == "blocked" {
		return printErr("Index request needs attention", errors.New(p.IndexAdvice.Message))
	}
	return 0
}

func validPipelineFreshnessDuration(d time.Duration) bool {
	return d == 0 || d >= time.Duration(api.DataPipelineFreshnessTargetMinSeconds)*time.Second && d <= time.Duration(api.DataPipelineFreshnessTargetMaxSeconds)*time.Second && d%time.Second == 0
}
