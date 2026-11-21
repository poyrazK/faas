package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"time"
)

func cmdRunEnvironments(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale run environments <build|list|get|delete|logs> [id]", "run")
		return 1
	}
	fs := newFlagSet("run environments", flag.ContinueOnError)
	follow := fs.Bool("follow", false, "follow durable diagnostic events until the environment settles")
	after := fs.Int64("after", 0, "resume diagnostics after this event sequence")
	runtime := fs.String("runtime", string(api.ExecutionRuntimePython313), "runtime for the dependency environment")
	requirements := fs.String("requirements", "", "hash-pinned Python requirements file")
	packageJSON := fs.String("package-json", "", "npm package.json file")
	packageLock := fs.String("package-lock", "", "npm lockfile v3")
	var systemPackages stringListFlag
	fs.Var(&systemPackages, "system-package", "exact Wolfi NAME=VERSION-rN package pin; repeatable")
	wait := fs.Bool("wait", false, "wait for an immutable ready environment")
	waitTimeout := fs.Duration("wait-timeout", time.Duration(api.ExecutionEnvironmentLeaseSeconds)*time.Second, "maximum client wait")
	flags, positional := splitArgsForFlags(args[1:], "wait", "follow")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	verb := args[0]
	if verb != "build" && verb != "list" && verb != "get" && verb != "delete" && verb != "logs" {
		return printErr("Invalid environment command", fmt.Errorf("use build, list, get, delete, or logs"))
	}
	if (verb == "get" || verb == "delete" || verb == "logs") && len(positional) != 1 || (verb == "build" || verb == "list") && len(positional) != 0 {
		return printErr("Invalid environment arguments", fmt.Errorf("get/delete require an environment ID"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if *after < 0 || *waitTimeout <= 0 {
		return printErr("Invalid environment arguments", fmt.Errorf("after must be non-negative and wait-timeout positive"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), *waitTimeout)
	defer cancel()
	if verb == "logs" {
		return runEnvironmentLogs(ctx, client, positional[0], *after, *follow)
	}
	if verb == "list" {
		rows, err := client.ListExecutionEnvironments(ctx)
		if err != nil {
			return printErr("Could not list environments", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(rows))
		}
		for _, row := range rows {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s\t%s\n", row.ID, row.Runtime, row.Status)
		}
		return 0
	}
	if verb == "delete" {
		if err := client.DeleteExecutionEnvironment(ctx, positional[0]); err != nil {
			return printErr("Could not retire environment", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]string{"status": "retiring"}))
		}
		PrintOK(osStdout, "Environment retirement requested.")
		return 0
	}
	var row api.ExecutionEnvironmentResponse
	if verb == "get" {
		row, err = client.GetExecutionEnvironment(ctx, positional[0])
	} else {
		request := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntime(*runtime), SystemPackages: []string(systemPackages)}
		request.Requirements, err = readEnvironmentManifest(*requirements)
		if err != nil {
			return printErr("Invalid requirements file", err)
		}
		request.PackageJSON, err = readEnvironmentManifest(*packageJSON)
		if err != nil {
			return printErr("Invalid package.json", err)
		}
		request.PackageLock, err = readEnvironmentManifest(*packageLock)
		if err != nil {
			return printErr("Invalid package lock", err)
		}
		row, err = client.CreateExecutionEnvironment(ctx, request)
	}
	if err != nil {
		return printErr("Environment request failed", err)
	}
	if *wait {
		row, err = waitExecutionEnvironment(ctx, client, row)
		if err != nil {
			return printErr("Environment build failed", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	PrintOK(osStdout, "Environment %s (runtime=%s, status=%s).", row.ID, row.Runtime, row.Status)
	return 0
}
func readEnvironmentManifest(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	file, err := openCustomerFile(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("manifest must be a regular file")
	}
	body, err := io.ReadAll(io.LimitReader(file, api.ExecutionEnvironmentRecipeMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > api.ExecutionEnvironmentRecipeMaxBytes {
		return "", fmt.Errorf("manifest exceeds %d bytes", api.ExecutionEnvironmentRecipeMaxBytes)
	}
	return string(body), nil
}
func waitExecutionEnvironment(ctx context.Context, client *api.Client, row api.ExecutionEnvironmentResponse) (api.ExecutionEnvironmentResponse, error) {
	ticker := time.NewTicker(api.ExecutionEnvironmentPollSeconds * time.Second)
	defer ticker.Stop()
	after := int64(0)
	diagnosticsAvailable := true
	for {
		if diagnosticsAvailable && !jsonOutput {
			page, err := client.ExecutionEnvironmentEvents(ctx, row.ID, after)
			if err != nil {
				diagnosticsAvailable = false
			} else {
				if page.Gap {
					_, _ = fmt.Fprintln(osStderr, "Earlier build diagnostics fell outside the retained tail.")
				}
				for _, e := range page.Events {
					_, _ = fmt.Fprintf(osStderr, "[%s #%d] %s\n", e.Stage, e.Sequence, e.Message)
				}
				after = page.NextSequence
			}
		}
		switch row.Status {
		case "ready":
			return row, nil
		case "failed", "retiring", "deleted":
			return row, fmt.Errorf("environment %s: %s", row.Status, row.FailureCode)
		}
		select {
		case <-ctx.Done():
			return row, ctx.Err()
		case <-ticker.C:
			next, err := client.GetExecutionEnvironment(ctx, row.ID)
			if err != nil {
				return row, err
			}
			row = next
		}
	}
}

func runEnvironmentLogs(ctx context.Context, client *api.Client, id string, after int64, follow bool) int {
	for {
		page, err := client.ExecutionEnvironmentEvents(ctx, id, after)
		if err != nil {
			return printErr("Could not read environment diagnostics", err)
		}
		if !follow && jsonOutput {
			return jsonOut(writeJSON(page))
		}
		if page.Gap {
			if jsonOutput {
				_ = writeJSON(map[string]any{"type": "gap", "after": after})
			} else {
				_, _ = fmt.Fprintln(osStderr, "Some earlier build diagnostics have expired from the retained tail.")
			}
		}
		for _, event := range page.Events {
			if jsonOutput {
				if err := writeJSON(event); err != nil {
					return 1
				}
			} else {
				_, _ = fmt.Fprintf(osStdout, "[%s #%d] %s\n", event.Stage, event.Sequence, event.Message)
			}
		}
		after = page.NextSequence
		if !follow || page.Status == "ready" || page.Status == "failed" || page.Status == "retiring" || page.Status == "deleted" {
			return 0
		}
		select {
		case <-ctx.Done():
			return printErr("Environment diagnostics wait ended", ctx.Err())
		case <-time.After(api.ExecutionEnvironmentPollSeconds * time.Second):
		}
	}
}
