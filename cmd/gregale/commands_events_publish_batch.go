package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsPublishBatch(args []string) int {
	flags, positional := splitArgsForFlags(args)
	fs := newFlagSet("events publish-batch", flag.ContinueOnError)
	input := fs.String("file", "", "JSONL file, or - for stdin; each event requires a stable id")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) != 0 || rejectUnexpectedFlagArgs(fs) || *input == "" {
		PrintUsage(os.Stderr, "usage: gregale events publish-batch --file <file.jsonl|->", "events")
		return 1
	}
	req, err := readEventPublishBatch(*input)
	if err != nil {
		return printErr("Invalid batch", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.PublishEventBatch(context.Background(), req)
	if err != nil {
		return printErr("Batch publish failed; retry the original file with the same ids", err)
	}
	exit := 0
	for _, result := range response.Results {
		if result.Status != "accepted" && result.Status != "duplicate" {
			exit = 1
		}
	}
	if jsonOutput {
		if code := jsonOut(writeJSON(response)); code != 0 {
			return code
		}
		return exit
	}
	printEventPublishBatchResults(response)
	return exit
}

func readEventPublishBatch(path string) (api.PublishEventBatchRequest, error) {
	var input io.Reader = os.Stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return api.PublishEventBatchRequest{}, err
		}
		defer func() { _ = file.Close() }()
		input = file
	}
	body, err := io.ReadAll(io.LimitReader(input, api.EventPublishBatchBodyMaxBytes+1))
	if err != nil {
		return api.PublishEventBatchRequest{}, err
	}
	if int64(len(body)) > api.EventPublishBatchBodyMaxBytes {
		return api.PublishEventBatchRequest{}, fmt.Errorf("JSONL input exceeds %d bytes", api.EventPublishBatchBodyMaxBytes)
	}
	req := api.PublishEventBatchRequest{Events: []api.PublishEventRequest{}}
	for line, raw := range bytes.Split(body, []byte{'\n'}) {
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var event api.PublishEventRequest
		if err := json.Unmarshal(raw, &event); err != nil {
			return req, fmt.Errorf("line %d is not an event envelope: %w", line+1, err)
		}
		if strings.TrimSpace(event.ID) == "" {
			return req, fmt.Errorf("line %d requires a stable id for safe retries", line+1)
		}
		req.Events = append(req.Events, event)
		if len(req.Events) > api.EventPublishBatchMaxEvents {
			return req, fmt.Errorf("batch exceeds %d events", api.EventPublishBatchMaxEvents)
		}
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return req, err
	}
	if len(req.Events) == 0 || int64(len(encoded)) > api.EventPublishBatchBodyMaxBytes {
		return req, fmt.Errorf("batch must be nonempty and its encoded request at most %d bytes", api.EventPublishBatchBodyMaxBytes)
	}
	return req, nil
}

func printEventPublishBatchResults(response api.PublishEventBatchResponse) {
	for _, result := range response.Results {
		detail := ""
		if result.Receipt != nil {
			detail = result.Receipt.ReceiptURL
		}
		if result.Problem != nil {
			detail = result.Problem.Code + ": " + result.Problem.Detail
		}
		_, _ = fmt.Fprintf(osStdout, "%d\t%s\tretryable=%t\t%s\n", result.Index, result.Status, result.Retryable, detail)
	}
}
