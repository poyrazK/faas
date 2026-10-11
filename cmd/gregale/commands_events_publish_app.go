package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdEventsPublishApp(args []string) int {
	flags, pos := splitArgsForFlags(args)
	fs := newFlagSet("events publish-app", flag.ContinueOnError)
	path := fs.String("file", "", "JSON with stable producer key, type and data")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 || *path == "" || rejectUnexpectedFlagArgs(fs) {
		return printErr("Invalid arguments", fmt.Errorf("use events publish-app APP --file PATH"))
	}
	req, err := readAppPublishEventRequest(*path)
	if err != nil {
		return printErr("Invalid event", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.EventRecoveryRequestTimeout)
	defer cancel()
	out, err := client.PublishAppEvent(ctx, pos[0], req)
	if err != nil {
		return printErr("Publish failed; retry with the same application, key and content", err)
	}
	return jsonOut(writeJSON(out))
}

func readAppPublishEventRequest(path string) (api.AppPublishEventRequest, error) {
	var req api.AppPublishEventRequest
	f, err := openCustomerFile(path)
	if err != nil {
		return req, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, api.AppEventPublishBodyMaxBytes+1))
	if err != nil {
		return req, err
	}
	if int64(len(raw)) > api.AppEventPublishBodyMaxBytes {
		return req, fmt.Errorf("event exceeds body limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return req, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return req, fmt.Errorf("expected one JSON object")
	}
	if err := req.Validate(); err != nil {
		return req, err
	}
	return req, nil
}
