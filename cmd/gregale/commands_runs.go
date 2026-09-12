package main

// Customer-facing disposable Runs commands (ADR-171).
//
// `gregale run` submits a single source file to a fresh Firecracker VM. The
// VM has loopback-only networking, an internal ephemeral scratch filesystem,
// and is destroyed after the terminal result; this command deliberately has
// no volume or persistent-workspace flags.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	executionPollIntervalDefault = 250 * time.Millisecond
	executionWaitTimeoutDefault  = 5 * time.Minute
)

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	runtimeName := fs.String("runtime", string(api.ExecutionRuntimeNode22), "isolated runtime (node22|node24|python312|python313)")
	source := fs.String("source", "", "source code (use --file for a local file)")
	file := fs.String("file", "", "read source from a local regular file")
	input := fs.String("input", "", "JSON input (inline | @file | - for stdin)")
	timeoutMS := fs.Int("timeout-ms", 0, "maximum execution time in milliseconds")
	memoryMB := fs.Int("memory-mb", 0, "memory limit in MB")
	cpuMillicores := fs.Int("cpu-millicores", 0, "CPU limit in millicores")
	diskMB := fs.Int("ephemeral-disk-mb", 0, "ephemeral scratch size in MB")
	maxOutputBytes := fs.Int("max-output-bytes", 0, "combined stdout/stderr/result cap")
	wait := fs.Bool("wait", false, "wait for the terminal result")
	pollInterval := fs.Duration("poll-interval", executionPollIntervalDefault, "status polling interval when --wait is set")
	waitTimeout := fs.Duration("wait-timeout", executionWaitTimeoutDefault, "maximum client wait duration")
	flags, positional := splitArgsForFlags(args, "wait")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || (*source == "" && *file == "") || (*source != "" && *file != "") {
		PrintUsage(osStderr, "usage: gregale run --runtime R (--source CODE | --file PATH) [--input J|@file|-] [--wait]", "run")
		return 1
	}
	if *pollInterval <= 0 || *waitTimeout <= 0 {
		PrintUsage(osStderr, "usage: gregale run ... [--poll-interval D] [--wait-timeout D]", "run")
		return 1
	}

	sourceBytes, err := executionSource(*source, *file)
	if err != nil {
		return printErr("Could not read source", err)
	}
	inputBytes, err := resolveExecutionInput(*input)
	if err != nil {
		return printErr("Invalid input", err)
	}
	req := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntime(*runtimeName),
		Source:  string(sourceBytes),
		Input:   inputBytes,
		Limits: &api.ExecutionLimitRequest{
			TimeoutMS:       *timeoutMS,
			MemoryMB:        *memoryMB,
			CPUMillicores:   *cpuMillicores,
			EphemeralDiskMB: *diskMB,
			MaxOutputBytes:  *maxOutputBytes,
		},
	}
	// Explicitly send the v1 deny-all policy. This keeps receipts and audit
	// records unambiguous and prevents a future server default from widening
	// the security posture of an older CLI binary.
	req.Network = &api.ExecutionNetworkPolicy{Mode: api.ExecutionNetworkNone}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CreateExecution(context.Background(), req)
	if err != nil {
		return printErr("Run submission failed", err)
	}
	if !*wait {
		if jsonOutput {
			return jsonOut(writeJSON(resp))
		}
		PrintOK(osStdout, "Run %s queued (status=%s).", resp.ID, resp.Status)
		return 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout)
	defer cancel()
	for !resp.Status.Terminal() {
		select {
		case <-ctx.Done():
			return printErr("Run wait timed out", ctx.Err())
		case <-time.After(*pollInterval):
		}
		resp, err = client.GetExecution(ctx, resp.ID)
		if err != nil {
			return printErr("Run status failed", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	return renderExecutionTerminal(resp)
}

func cmdRuns(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale runs <get|status|cancel> <id>", "runs")
		return 1
	}
	verb := args[0]
	if verb != "get" && verb != statusLiteral && verb != "cancel" {
		PrintUsage(osStderr, "usage: gregale runs <get|status|cancel> <id>", "runs")
		return 1
	}
	flags, positional := splitArgsForFlags(args[1:])
	if len(flags) != 0 || len(positional) != 1 {
		PrintUsage(osStderr, "usage: gregale runs <get|status|cancel> <id>", "runs")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var resp api.ExecutionResponse
	if verb == "cancel" {
		resp, err = client.CancelExecution(ctx, positional[0])
	} else {
		resp, err = client.GetExecution(ctx, positional[0])
	}
	if err != nil {
		return printErr("Run request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if verb == "cancel" {
		PrintOK(osStdout, "Run %s cancellation requested (status=%s).", resp.ID, resp.Status)
		return 0
	}
	if resp.Status.Terminal() {
		return renderExecutionTerminal(resp)
	}
	PrintProgress(osStdout, "Run %s status=%s.", resp.ID, resp.Status)
	return 0
}

func executionSource(inline, path string) ([]byte, error) {
	if inline != "" {
		return []byte(inline), nil
	}
	f, err := openCustomerFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, int64(api.ExecutionSealedPayloadMaxBytes)+1))
}

// resolveExecutionInput mirrors the CLI's inline/@file/stdin input shapes but
// keeps customer file reads behind the symlink-safe openCustomerFile boundary.
// A run can contain attacker-controlled input, so silently following a local
// symlink here would turn a submission into an unintended file exfiltration.
func resolveExecutionInput(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if s[0] == '@' {
		f, err := openCustomerFile(s[1:])
		if err != nil {
			return nil, fmt.Errorf("read input file %q: %w", s[1:], err)
		}
		defer func() { _ = f.Close() }()
		return io.ReadAll(io.LimitReader(f, int64(api.ExecutionSealedPayloadMaxBytes)+1))
	}
	if s == "-" {
		return io.ReadAll(osStdin)
	}
	return []byte(s), nil
}

func renderExecutionTerminal(resp api.ExecutionResponse) int {
	if resp.Status == api.ExecutionStatusSucceeded {
		PrintOK(osStdout, "Run %s succeeded.", resp.ID)
	} else {
		PrintFail(osStdout, "Run %s finished with status=%s.", resp.ID, resp.Status)
	}
	if resp.Result != nil {
		_, _ = fmt.Fprintln(osStdout, string(resp.Result))
	}
	if resp.Stdout != "" {
		_, _ = fmt.Fprint(osStdout, resp.Stdout)
	}
	if resp.Stderr != "" {
		_, _ = fmt.Fprint(osStderr, resp.Stderr)
	}
	if resp.Status != api.ExecutionStatusSucceeded {
		return 1
	}
	return 0
}
