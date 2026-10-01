package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdProjectsEnvironmentQueues(args []string) int {
	const usage = "usage: gregale projects environments queues <get|set> <project> <stage> <workload> [--file PATH|--stdin]"
	if len(args) == 0 || (args[0] != "get" && args[0] != "set") {
		PrintUsage(os.Stderr, usage, "projects environments")
		return 1
	}
	flags, positional := splitArgsForFlags(args[1:], "file", "stdin")
	fs := newFlagSet("projects-environments-queues", flag.ContinueOnError)
	file := fs.String("file", "", "JSON document containing expected_revision and the complete bindings list")
	stdin := fs.Bool("stdin", false, "read queue configuration from stdin")
	if err := fs.Parse(flags); err != nil || len(positional) != 3 || !api.ValidProjectSlug(positional[0]) || !api.ValidProjectEnvironmentSlug(positional[1]) || positional[1] == "production" || !api.ValidAppSlug(positional[2]) ||
		(args[0] == "get" && (*file != "" || *stdin)) || (args[0] == "set" && ((*file == "") == !*stdin)) {
		PrintUsage(os.Stderr, usage, "projects environments")
		return 1
	}
	var request api.ReplaceProjectEnvironmentQueueBindingsRequest
	if args[0] == "set" {
		var err error
		request, err = readProjectEnvironmentQueuesInput(*file, *stdin)
		if err != nil {
			return printErr("Invalid stage queue configuration", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var response api.ProjectEnvironmentQueueBindingsResponse
	if args[0] == "get" {
		response, err = client.GetProjectEnvironmentQueueBindings(context.Background(), positional[0], positional[1], positional[2])
	} else {
		response, err = client.ReplaceProjectEnvironmentQueueBindings(context.Background(), positional[0], positional[1], positional[2], request)
	}
	if err != nil {
		return printErr("Could not load or save stage queue configuration", err)
	}
	if jsonOutput || args[0] == "get" {
		return jsonOut(writeJSON(response))
	}
	_, _ = fmt.Fprintf(osStdout, "Saved %s/%s/%s queue configuration: %d bindings, workload revision %d (consumer activation: %s)\n", positional[0], positional[1], positional[2], len(response.Bindings), response.WorkloadRevision, response.ActivationState)
	return 0
}

func readProjectEnvironmentQueuesInput(file string, stdin bool) (api.ReplaceProjectEnvironmentQueueBindingsRequest, error) {
	var request api.ReplaceProjectEnvironmentQueueBindingsRequest
	raw, err := readProjectEnvironmentConfigInput(file, stdin)
	if err != nil {
		return request, err
	}
	if len(raw) > api.MaxProjectEnvironmentConfigBytes {
		return request, errors.New("queue configuration exceeds the environment configuration document size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return request, errors.New("queue configuration must contain one JSON document")
	}
	if request.ExpectedRevision == nil || *request.ExpectedRevision < 0 || request.Bindings == nil {
		return request, errors.New("expected_revision and the complete bindings list are required; use [] to remove stage bindings")
	}
	return request, nil
}
