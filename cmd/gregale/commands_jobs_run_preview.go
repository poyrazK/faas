package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This is a local plan: remote Job defaults and account limits are not fetched.
func previewJobRun(name string, req api.CreateJobRunRequest, wait, collect bool, timeout, interval time.Duration, directory, defaultParent string, includes, excludes []string, waitReady bool, imageTimeout, imageInterval time.Duration) int {
	if !jobSlugPattern.MatchString(name) {
		return printErr("Invalid Job name", fmt.Errorf("provide a valid Job name"))
	}
	if len(req.Inputs) > 0 && req.Tasks != 0 && req.Tasks != len(req.Inputs) {
		return printErr("Invalid task count", fmt.Errorf("--tasks must match the number of input bindings"))
	}
	taskCount := req.Tasks
	taskSource := "tasks"
	if len(req.Inputs) > 0 {
		taskCount = len(req.Inputs)
		taskSource = "input_bindings"
	}
	if req.InputManifestURI != "" {
		taskSource = "input_manifest"
	}
	// Copy the request and its environment map; never mutate submission data.
	sanitized := req
	keys := make([]string, 0, len(req.EnvOverrides))
	if len(req.EnvOverrides) > 0 {
		sanitized.EnvOverrides = make(map[string]string, len(req.EnvOverrides))
		for key := range req.EnvOverrides {
			keys = append(keys, key)
			sanitized.EnvOverrides[key] = "[redacted]"
		}
		sort.Strings(keys)
	}
	argumentCount := 0
	if req.Arguments != nil {
		argumentCount = len(*req.Arguments)
		sanitized.Arguments = nil
	}
	if len(req.Inputs) > 0 {
		sanitized.Inputs = append([]api.JobRunInput(nil), req.Inputs...)
		for i := range sanitized.Inputs {
			sanitized.Inputs[i].Ref = "[redacted]"
		}
	}
	if sanitized.InputManifestURI != "" {
		sanitized.InputManifestURI = "[redacted]"
	}
	outputPattern := ""
	if collect {
		outputPattern = directory
		if defaultParent != "" {
			outputPattern = filepath.Join(defaultParent, "<run-id>")
		}
	}
	receipt := struct {
		WaitReady         bool                    `json:"wait_ready"`
		ImageWaitTimeout  string                  `json:"image_wait_timeout,omitempty"`
		ImagePollInterval string                  `json:"image_poll_interval,omitempty"`
		DryRun            bool                    `json:"dry_run"`
		Job               string                  `json:"job"`
		TaskCount         int                     `json:"task_count"`
		TaskCountSource   string                  `json:"task_count_source"`
		Request           api.CreateJobRunRequest `json:"request_preview"`
		ArgumentCount     int                     `json:"argument_override_count"`
		Wait              bool                    `json:"wait"`
		WaitTimeout       string                  `json:"wait_timeout,omitempty"`
		PollInterval      string                  `json:"poll_interval,omitempty"`
		Collect           bool                    `json:"collect_results"`
		OutputPattern     string                  `json:"output_directory_pattern,omitempty"`
		Include           []string                `json:"include,omitempty"`
		Exclude           []string                `json:"exclude,omitempty"`
		Note              string                  `json:"note"`
	}{WaitReady: waitReady, DryRun: true, Job: name, TaskCount: taskCount, TaskCountSource: taskSource, Request: sanitized, ArgumentCount: argumentCount, Wait: wait, Collect: collect, OutputPattern: outputPattern, Include: includes, Exclude: excludes, Note: "Local preview only; remote Job defaults, manifest contents, readiness, and account limits are not checked. Environment values, argument values, and input references are redacted. No run submitted or files written."}
	if waitReady {
		receipt.ImageWaitTimeout = imageTimeout.String()
		receipt.ImagePollInterval = imageInterval.String()
	}
	if wait {
		receipt.WaitTimeout = timeout.String()
		receipt.PollInterval = interval.String()
	}
	if jsonOutput {
		return jsonOut(writeJSONSingle(receipt))
	}
	_, _ = fmt.Fprintf(osStdout, "Job run preview: %s\n", name)
	if taskSource == "input_manifest" {
		_, _ = fmt.Fprintln(osStdout, "Tasks: determined by input manifest (not read)")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Tasks: %d (from %s)\n", taskCount, taskSource)
	}
	for _, v := range []struct {
		name  string
		value *int
	}{{"Parallelism", req.Parallelism}, {"Retries", req.RetryMax}, {"Task timeout (seconds)", req.TaskTimeoutSec}} {
		if v.value == nil {
			_, _ = fmt.Fprintf(osStdout, "%s: remote Job default (not read)\n", v.name)
		} else {
			_, _ = fmt.Fprintf(osStdout, "%s: %d\n", v.name, *v.value)
		}
	}
	_, _ = fmt.Fprintf(osStdout, "Environment overrides: %d (values redacted)\nArgument overrides: %d (values redacted)\n", len(keys), argumentCount)
	for _, key := range keys {
		_, _ = fmt.Fprintf(osStdout, "  %s=[redacted]\n", recoveryArgument(key))
	}
	if req.ExecutionClass != "" {
		_, _ = fmt.Fprintf(osStdout, "Execution class: %s\n", req.ExecutionClass)
	}
	if req.FailurePolicy != "" {
		_, _ = fmt.Fprintf(osStdout, "Failure policy: %s\n", req.FailurePolicy)
	}
	if req.EligibleAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Eligible at: %s\n", req.EligibleAt.Format(time.RFC3339))
	}
	if req.LatestStartAt != nil {
		_, _ = fmt.Fprintf(osStdout, "Latest start at: %s\n", req.LatestStartAt.Format(time.RFC3339))
	}
	if req.FailureRules != nil {
		_, _ = fmt.Fprintln(osStdout, "Failure rules: overridden (see JSON preview for rules)")
	}
	_, _ = fmt.Fprintf(osStdout, "Wait for image before submission: %t\n", waitReady)
	if waitReady {
		_, _ = fmt.Fprintf(osStdout, "Image wait timeout: %s; poll interval: %s\n", imageTimeout, imageInterval)
	}
	_, _ = fmt.Fprintf(osStdout, "Wait: %t\nCollect results: %t\n", wait, collect)
	if wait {
		_, _ = fmt.Fprintf(osStdout, "Wait timeout: %s; poll interval: %s\n", timeout, interval)
	}
	if collect {
		_, _ = fmt.Fprintf(osStdout, "Results destination: %s\n", outputPattern)
		printSavedJobArtifactFilters(localJobDefaults{Include: includes, Exclude: excludes})
		if len(includes) == 0 && len(excludes) == 0 {
			_, _ = fmt.Fprintln(osStdout, "Artifacts: all managed outputs")
		}
	}
	_, _ = fmt.Fprintln(osStdout, receipt.Note)
	return 0
}
