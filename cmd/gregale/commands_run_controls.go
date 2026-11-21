package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func readRunControlManifest(path string, dst any) error {
	f, err := openCustomerFile(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	d := json.NewDecoder(io.LimitReader(f, api.RunControlBodyMaxBytes+1))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("one bounded JSON object required")
	}
	return nil
}
func cmdRunControls(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale run controls <customer|task|binding|unbind> KIND_OR_REF [ID] [--manifest PATH]", "run")
		return 1
	}
	verb := args[0]
	fs := newFlagSet("run controls", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "complete revisioned policy or binding JSON")
	revision := fs.Int64("expected-revision", 0, "current binding revision for removal")
	flags, pos := splitArgsForFlags(args[1:])
	if fs.Parse(flags) != nil {
		return 1
	}
	if (verb == "customer" || verb == "task") && len(pos) != 1 || (verb == "binding" || verb == "unbind") && len(pos) != 2 {
		return printErr("Invalid controls arguments", fmt.Errorf("customer/task require REF; binding/unbind require api_key|agent_endpoint ID"))
	}
	if verb != "customer" && verb != "task" && verb != "binding" && verb != "unbind" {
		return printErr("Invalid controls command", fmt.Errorf("use customer, task, binding or unbind"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out any
	switch verb {
	case "customer", "task":
		if *manifest == "" {
			out, err = client.GetRunBudget(ctx, verb, pos[0])
		} else {
			var req api.SetRunBudgetRequest
			if e := readRunControlManifest(*manifest, &req); e != nil {
				return printErr("Invalid budget manifest", e)
			}
			out, err = client.SetRunBudget(ctx, verb, pos[0], req)
		}
	case "binding":
		if *manifest == "" {
			out, err = client.GetRunAttributionBinding(ctx, pos[0], pos[1])
		} else {
			var req api.SetRunAttributionBindingRequest
			if e := readRunControlManifest(*manifest, &req); e != nil {
				return printErr("Invalid binding manifest", e)
			}
			out, err = client.SetRunAttributionBinding(ctx, pos[0], pos[1], req)
		}
	case "unbind":
		if *revision < 1 {
			return printErr("Revision required", fmt.Errorf("use --expected-revision with the current revision"))
		}
		err = client.DeleteRunAttributionBinding(ctx, pos[0], pos[1], *revision)
		out = map[string]string{"status": "disabled"}
	}
	if err != nil {
		return printErr("Run controls request failed", err)
	}
	return writeRunControlOutput(out)
}
func writeRunControlOutput(out any) int {
	enc := json.NewEncoder(osStdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return printErr("Output failed", err)
	}
	return 0
}
func cmdRunHistory(args []string) int {
	fs := newFlagSet("run history", flag.ContinueOnError)
	var f api.RunHistoryFilter
	var status, since, until string
	fs.StringVar(&f.TaskName, "task-name", "", "published task name")
	fs.IntVar(&f.TaskVersion, "task-version", 0, "immutable version; requires task-name")
	fs.StringVar(&f.CustomerRef, "customer-ref", "", "authenticated customer reference")
	fs.StringVar(&f.AgentRef, "agent-ref", "", "assigned agent reference")
	fs.StringVar(&status, "status", "", "Run lifecycle status")
	fs.StringVar(&since, "since", "", "inclusive RFC 3339 admission timestamp")
	fs.StringVar(&until, "until", "", "exclusive RFC 3339 admission timestamp")
	fs.StringVar(&f.Cursor, "cursor", "", "next_cursor from the preceding page")
	if fs.Parse(args) != nil {
		return 1
	}
	if fs.NArg() != 0 {
		return printErr("Invalid history arguments", fmt.Errorf("use filter flags"))
	}
	f.Status = api.ExecutionStatus(status)
	var err error
	if since != "" {
		f.Since, err = time.Parse(time.RFC3339Nano, since)
		if err != nil {
			return printErr("Invalid since timestamp", err)
		}
	}
	if until != "" {
		f.Until, err = time.Parse(time.RFC3339Nano, until)
		if err != nil {
			return printErr("Invalid until timestamp", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := client.ListRunHistory(ctx, f)
	if err != nil {
		return printErr("Run history request failed", err)
	}
	return writeRunControlOutput(out)
}
