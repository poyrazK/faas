package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtask"
)

func cmdRunTasks(args []string) int {
	if len(args) == 0 {
		PrintUsage(osStderr, "usage: gregale run tasks <validate|generate|test|publish|list|get|retire|invoke>", "run")
		return 1
	}
	verb := args[0]
	switch verb {
	case "validate", "generate":
		return cmdRunTaskLocal(verb, args[1:])
	case "test":
		return cmdRunTaskTest(args[1:])
	}
	fs := newFlagSet("run tasks", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "publication JSON file containing name, version and definition")
	customerRef := fs.String("customer-ref", "", "customer policy; bound callers keep their authenticated customer")
	input := fs.String("input", "null", "invocation JSON or @file")
	key := fs.String("idempotency-key", "", "stable invocation key; generated and printed when omitted")
	wait := fs.Bool("wait", false, "wait for the terminal receipt")
	timeout := fs.Duration("wait-timeout", 6*time.Minute, "maximum client wait")
	cursor := fs.String("cursor", "", "catalog pagination cursor")
	output := fs.String("output-dir", "", "save output artifacts; implies wait")
	var inputIDs executionInputFileIDFlags
	fs.Var(&inputIDs, "input-file-id", "RELATIVE_INPUT_PATH=FILE_UUID (repeatable)")
	flags, pos := splitArgsForFlags(args[1:], "wait")
	if fs.Parse(flags) != nil {
		return 1
	}
	if *timeout <= 0 {
		return printErr("Invalid timeout", fmt.Errorf("wait-timeout must be positive"))
	}
	if verb != "publish" && verb != "list" && verb != "get" && verb != "retire" && verb != "invoke" {
		return printErr("Invalid task command", fmt.Errorf("use validate, generate, test, publish, list, get, retire or invoke"))
	}
	needsRef := verb == "get" || verb == "retire" || verb == "invoke"
	if needsRef && len(pos) != 1 || !needsRef && len(pos) != 0 {
		return printErr("Invalid task arguments", fmt.Errorf("get, retire and invoke require name@vN"))
	}
	var name string
	var version int
	if needsRef {
		var raw string
		var ok bool
		name, raw, ok = strings.Cut(pos[0], "@v")
		version, _ = strconv.Atoi(raw)
		if !ok || !runtask.ValidRef(name, version) {
			return printErr("Invalid task reference", fmt.Errorf("expected name@vN"))
		}
	}
	if verb == "invoke" {
		*key = strings.TrimSpace(*key)
		if err := validateDeployIdempotencyKey(*key); err != nil {
			return printErr("Invalid idempotency key", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	var result any
	switch verb {
	case "publish":
		pub, err := loadRunTaskManifest(*manifest)
		if err != nil {
			return printErr("Invalid task manifest", err)
		}
		result, err = client.PublishExecutionTask(ctx, pub)
		if err != nil {
			return printErr("Publication failed", err)
		}
	case "list":
		result, err = client.ListExecutionTasks(ctx, *cursor)
	case "get":
		result, err = client.GetExecutionTask(ctx, name, version)
	case "retire":
		err = client.RetireExecutionTask(ctx, name, version)
		result = map[string]string{"status": "retired"}
	case "invoke":
		data, e := resolveExecutionInput(*input)
		if e != nil {
			return printErr("Invalid input", e)
		}
		if *key == "" {
			*key = uuid.NewString()
			_, _ = fmt.Fprintln(osStderr, "Idempotency-Key:", *key)
		}
		var row api.ExecutionResponse
		row, err = client.InvokeExecutionTask(ctx, api.InvokeExecutionTaskRequest{CustomerRef: *customerRef, Name: name, Version: version, Input: data, InputFiles: inputIDs}, *key)
		if err != nil {
			return printRunSubmissionError("Task invocation failed", err, "gregale sandbox list", true)
		}
		for (*wait || *output != "") && !row.Status.Terminal() {
			select {
			case <-ctx.Done():
				return printErr("Task wait timed out", ctx.Err())
			case <-time.After(time.Second):
			}
			row, err = client.GetExecution(ctx, row.ID)
			if err != nil {
				return printErr("Task status failed", err)
			}
		}
		if err = saveExecutionArtifactsWithClient(ctx, client, row, *output); err != nil {
			return printErr("Artifact download failed", err)
		}
		if jsonOutput {
			if code := jsonOut(writeJSON(row)); code != 0 {
				return code
			}
		} else {
			_, _ = fmt.Fprintf(osStdout, "%s\t%s@v%d\t%s\n", row.ID, name, version, row.Status)
		}
		if row.Status.Terminal() && (row.Status != api.ExecutionStatusSucceeded || row.ContractFailed()) {
			return 1
		}
		return 0
	}
	if err != nil {
		return printErr("Task operation failed", err)
	}
	return jsonOut(writeJSON(result))
}
