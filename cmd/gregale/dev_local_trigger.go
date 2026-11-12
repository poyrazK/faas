package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

const devLocalTriggerUsage = "usage: gregale dev trigger --local [--path DIR] <cron|invoke> FUNCTION [REQUEST_PATH] [--method M] [--data BODY]"

// cmdDevTriggerLocal calls a function served by `gregale dev --local` the way
// a cron or an async invocation reaches it on Gregale: with the platform's
// x-faas-invocation-source and attempt headers (ADR-20261107210000).
func cmdDevTriggerLocal(args []string) int {
	fs := newFlagSet("dev trigger --local", flag.ContinueOnError)
	sourcePath := fs.String("path", "", "source directory of the running gregale dev --local")
	method := fs.String("method", "POST", "HTTP method")
	data := fs.String("data", "", "request body")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() < 2 || fs.NArg() > 3 {
		PrintUsage(osStderr, devLocalTriggerUsage, "dev")
		return 1
	}
	verb, function := fs.Arg(0), fs.Arg(1)
	source := map[string]string{"cron": "cron", "invoke": "async_invoke"}[verb]
	if source == "" {
		PrintUsage(osStderr, devLocalTriggerUsage, "dev")
		return 1
	}
	requestPath := "/"
	if fs.NArg() == 3 {
		requestPath = fs.Arg(2)
	}
	// A trigger addresses the running process, including while its env file
	// has an invalid edit; it does not consume that file itself.
	projectDir, _, _, err := devLocalInputs(*sourcePath, "", true)
	if err != nil {
		return printErr("Could not trigger", err)
	}
	st, err := readDevLocalState(projectDir)
	if err != nil {
		return printErr("Could not trigger", err)
	}
	body, _ := json.Marshal(devLocalTrigger{Function: function, Source: source, Method: *method, Path: requestPath, Body: *data})
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(st.Port)+devLocalTriggerPath, bytes.NewReader(body))
	if err != nil {
		return printErr("Could not trigger", err)
	}
	req.Header.Set(devLocalTokenHeader, st.Token)
	req.Header.Set("content-type", "application/json")
	resp, err := (&http.Client{Timeout: 16 * time.Minute}).Do(req)
	if err != nil {
		return printErr("Could not reach gregale dev --local", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		var msg bytes.Buffer
		_, _ = msg.ReadFrom(resp.Body)
		return printErr("Trigger refused", fmt.Errorf("%s", bytes.TrimSpace(msg.Bytes())))
	}
	var result devLocalTriggerResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return printErr("Could not read the result", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "%s %s → %d\n%s\n", verb, function, result.Status, result.Body)
	if result.Status >= 400 {
		return 1
	}
	return 0
}
