package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func pipelineCreateKey(req api.CreateDataPipelineRequest) string {
	parts := []string{req.Name, req.DatabaseID, req.SourceConnectionID}
	if req.Serving != nil || req.FreshnessTargetSeconds != 0 {
		b, _ := json.Marshal(datapipeline.NormalizeCreateRequest(req))
		parts = append(parts, string(b))
	}
	return addResourceIdempotencyKey("pipeline", parts...)
}

func readPipelineJSON(path string, target any) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, api.DataPipelineRequestMaxBytes+1))
	if err != nil {
		return err
	}
	if len(b) > api.DataPipelineRequestMaxBytes {
		return errors.New("pipeline configuration exceeds the request limit")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(target); err != nil {
		return err
	}
	if err = d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("supply exactly one JSON value")
	}
	return nil
}

func readPipelineServing(path string) (*api.DataPipelineServing, error) {
	if path == "" {
		return nil, nil
	}
	var serving *api.DataPipelineServing
	if err := readPipelineJSON(path, &serving); err != nil {
		return nil, err
	}
	if serving == nil {
		return nil, errors.New("serving configuration must be a JSON object")
	}
	return serving, nil
}

func cmdPipelineTenantAccess(args []string) int {
	fs := newFlagSet("pipeline access", flag.ContinueOnError)
	file := fs.String("members-file", "", "preview the complete subject/tenant membership array from a JSON file")
	approve := fs.String("approve", "", "approve the exact saved change ID")
	discard := fs.Bool("discard", false, "discard an unapproved membership preview")
	revision := fs.Int64("revision", 0, "expected pipeline revision; defaults to the current revision")
	wait := fs.Bool("wait", false, "wait for an approved policy update to be acknowledged")
	timeout := fs.Duration("timeout", 5*time.Minute, "maximum approval wait, up to 1h")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	choices := 0
	for _, set := range []bool{*file != "", *approve != "", *discard} {
		if set {
			choices++
		}
	}
	if fs.NArg() != 1 || choices != 1 || *revision < 0 || *timeout <= 0 || *timeout > time.Hour || *wait && *approve == "" {
		return printErr("Invalid tenant access request", errors.New("supply a pipeline ID and exactly one of --members-file, --approve or --discard; --wait requires --approve"))
	}
	id, err := canonicalPipelineID(fs.Arg(0))
	if err != nil {
		return printErr("Invalid pipeline ID", err)
	}
	r := api.DataPipelineTenantReadersRequest{Action: "preview", ChangeID: *approve}
	if *file != "" {
		if err = readPipelineJSON(*file, &r.Memberships); err != nil {
			return printErr("Could not read memberships", err)
		}
		if r.Memberships == nil {
			return printErr("Invalid memberships", errors.New("supply a JSON array; use [] to revoke all access"))
		}
	}
	if *approve != "" {
		r.Action = "approve"
	}
	if *discard {
		r.Action = "discard"
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	current, err := client.GetDataPipeline(ctx, id)
	if err != nil {
		return printErr("Could not inspect pipeline", err)
	}
	r.ExpectedRevision = *revision
	if r.ExpectedRevision == 0 {
		r.ExpectedRevision = current.Revision
	}
	p, err := client.ChangeDataPipelineTenantReaders(ctx, id, r)
	if err != nil {
		return printErr("Could not change tenant memberships", err)
	}
	if *wait {
		p, err = waitPipelineTenantAccess(ctx, client, p, *approve)
	}
	if writeErr := writeJSON(p); writeErr != nil {
		return jsonOut(writeErr)
	}
	if err != nil {
		return printErr("Membership approval is retained for retry", err)
	}
	return 0
}

func waitPipelineTenantAccess(ctx context.Context, client *api.Client, p api.DataPipeline, approval string) (api.DataPipeline, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if p.TenantChange == nil || p.TenantChange.ID != approval {
			return p, errors.New("the saved access change no longer matches; inspect pipeline status")
		}
		if p.TenantChange.State == "applied" {
			return p, nil
		}
		select {
		case <-ctx.Done():
			return p, ctx.Err()
		case <-ticker.C:
		}
		next, err := client.GetDataPipeline(ctx, p.ID)
		if err != nil {
			return p, err
		}
		p = next
	}
}
