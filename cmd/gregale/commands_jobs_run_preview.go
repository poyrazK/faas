package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type jobRunPreviewDefaults struct {
	Path       string `json:"path"`
	Saved      bool   `json:"saved"`
	MatchesJob bool   `json:"matches_job"`
	Applied    bool   `json:"applied"`
}

// This is a local plan: remote Job defaults and account limits are not fetched.
func previewJobRun(name string, req api.CreateJobRunRequest, wait, collect bool, timeout, interval time.Duration, directory, defaultParent string, includes, excludes []string, waitReady bool, imageTimeout, imageInterval time.Duration, sources map[string]string, localDefaults jobRunPreviewDefaults) int {
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
		Connection        previewConnection       `json:"connection"`
		LocalDefaults     jobRunPreviewDefaults   `json:"local_defaults"`
		SettingSources    map[string]string       `json:"setting_sources"`
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
	receipt.SettingSources = sources
	receipt.Connection = configuredPreviewConnection()
	receipt.LocalDefaults = localDefaults
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
	renderPreviewConnection(receipt.Connection)
	_, _ = fmt.Fprintf(osStdout, "Local defaults file: %s\nDefaults saved: %t; match selected Job: %t; applied settings: %t\n", oneLine(localDefaults.Path), localDefaults.Saved, localDefaults.MatchesJob, localDefaults.Applied)
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
	_, _ = fmt.Fprintln(osStdout, "Setting sources:")
	for _, setting := range []struct{ key, label string }{
		{"job", "Job"}, {"task_count", "Task count"}, {"collect_results", "Result collection"},
		{"artifact_filters", "Artifact filters"}, {"wait_ready", "Image wait"}, {"wait", "Run wait"},
		{"parallelism", "Parallelism"}, {"retries", "Retries"}, {"task_timeout", "Task timeout"},
	} {
		_, _ = fmt.Fprintf(osStdout, "  %s: %s\n", setting.label, jobRunSettingSourceLabel(sources[setting.key]))
	}
	return 0
}

// Capture sources at the same point defaults are applied, without re-reading
// configuration or exposing input, environment, or argument values.
func jobRunSettingSources(fs *flag.FlagSet, allArtifacts bool) map[string]string {
	sources := map[string]string{
		"job": "explicit_name", "task_count": "cli_default", "collect_results": "cli_default",
		"artifact_filters": "cli_default", "wait_ready": "cli_default", "wait": "cli_default",
		"parallelism": "remote_default_unread", "retries": "remote_default_unread", "task_timeout": "remote_default_unread",
	}
	fs.Visit(func(f *flag.Flag) {
		key := map[string]string{"tasks": "task_count", "results": "collect_results", "out-dir": "collect_results", "include": "artifact_filters", "exclude": "artifact_filters", "wait-ready": "wait_ready", "wait": "wait"}[f.Name]
		if key != "" {
			sources[key] = "explicit_flag"
		}
	})
	if allArtifacts {
		sources["artifact_filters"] = "explicit_flag"
	}
	return sources
}

func finishJobRunSettingSources(sources map[string]string, req api.CreateJobRunRequest, results, outDirSet, collect bool) {
	if len(req.Inputs) > 0 {
		sources["task_count"] = "input_bindings"
	}
	if req.InputManifestURI != "" {
		sources["task_count"] = "input_manifest_unread"
	}
	if req.Parallelism != nil {
		sources["parallelism"] = "explicit_flag"
	}
	if req.RetryMax != nil {
		sources["retries"] = "explicit_flag"
	}
	if req.TaskTimeoutSec != nil {
		sources["task_timeout"] = "explicit_flag"
	}
	if results {
		sources["wait"] = "implied_results"
	}
	if outDirSet {
		sources["wait"] = "implied_output_directory"
	}
	if !collect {
		sources["artifact_filters"] = "not_applicable"
	}
}

func jobRunSettingSourceLabel(source string) string {
	labels := map[string]string{
		"explicit_name": "explicit Job name", "explicit_flag": "explicit flag",
		"saved_defaults": "saved Job defaults", "cli_default": "CLI default",
		"remote_default_unread": "remote Job default (not read)", "input_bindings": "explicit input bindings",
		"input_manifest_unread": "input manifest (not read)", "implied_results": "implied by result collection",
		"implied_output_directory": "implied by output directory", "not_applicable": "not collecting artifacts",
	}
	return labels[source]
}
