package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"strconv"
	"strings"
)

func cmdPipelineWatch(args []string) int {
	if len(args) != 1 && len(args) != 3 || len(args) == 3 && args[1] != "--after" {
		return printErr("Invalid arguments", fmt.Errorf("usage: gregale sandbox workflow watch <pipeline-id> [--after CURSOR] [--json]"))
	}
	var after int64
	var err error
	if len(args) == 3 {
		after, err = strconv.ParseInt(args[2], 10, 64)
		if err != nil || after < 0 {
			return printErr("Invalid cursor", fmt.Errorf("after must be non-negative"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	err = client.WatchExecutionPipeline(context.Background(), args[0], after, func(page api.ExecutionPipelineEventsResponse) error {
		if jsonOutput {
			return writeJSON(page)
		}
		if page.Gap {
			PrintProgress(osStdout, "Progress history gap; current snapshot at cursor %d.", page.NextSequence)
		}
		for _, e := range page.Events {
			if e.Step != nil {
				PrintProgress(osStdout, "[%d] %s: %s %s (Run %s)", e.Sequence, e.Step.Label, e.Step.State, e.Step.Reason, e.Step.RunID)
			} else {
				PrintProgress(osStdout, "[%d] Pipeline %s", e.Sequence, e.Status)
			}
		}
		return nil
	})
	if err != nil {
		return printErr("Pipeline watch failed", err)
	}
	return 0
}
func cmdPipelineRerun(args []string) int {
	if len(args) != 3 && len(args) != 5 || len(args) == 5 && args[3] != "--repeat-steps" {
		return printErr("Invalid arguments", fmt.Errorf("usage: gregale sandbox workflow retry <pipeline-id> <new-workflow-id> <original-manifest.json> [--repeat-steps a,b] [--json]"))
	}
	file, err := openWorkflowPlanFile(args[2])
	if err != nil {
		return printErr("Could not open original manifest", err)
	}
	defer file.Close() //nolint:errcheck
	raw, err := io.ReadAll(io.LimitReader(file, api.ExecutionWorkflowManagedPlanMaxBytes+1))
	if err != nil || len(raw) > api.ExecutionWorkflowManagedPlanMaxBytes {
		return printErr("Invalid manifest", fmt.Errorf("manifest exceeds size limit or cannot be read"))
	}
	var original api.CreateManagedExecutionWorkflowRequest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&original); err != nil {
		return printErr("Invalid manifest", err)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return printErr("Invalid manifest", fmt.Errorf("one JSON value required"))
	}
	request := api.RerunExecutionPipelineRequest{WorkflowID: args[1], Original: original}
	if len(args) == 5 {
		request.RepeatSteps = strings.Split(args[4], ",")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.RerunExecutionPipeline(context.Background(), args[0], request)
	if err != nil {
		return printErr("Pipeline rerun failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Pipeline %s accepted (id %s).", out.WorkflowID, out.ID)
	return 0
}
