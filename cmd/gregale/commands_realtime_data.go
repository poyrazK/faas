package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// realtimeCLIResult is the stable, non-payload result returned by mutating
// realtime CLI operations. Message bytes are deliberately never echoed.
type realtimeCLIResult struct {
	Operation    string `json:"operation"`
	AppSlug      string `json:"app_slug"`
	EndpointID   string `json:"endpoint_id"`
	ConnectionID string `json:"connection_id,omitempty"`
	Channel      string `json:"channel,omitempty"`
	Queued       *int   `json:"queued,omitempty"`
}

func cmdRealtimeSend(args []string) int {
	args = normalizeRealtimeMessageArgs(args)
	fs := newFlagSet("realtime send", flag.ContinueOnError)
	data := fs.String("data", "", "UTF-8 message data (prefer --data-stdin for binary-safe input)")
	dataStdin := fs.Bool("data-stdin", false, "read message bytes from stdin")
	binary := fs.Bool("binary", false, "send the message as a binary WebSocket frame")
	if err := fs.Parse(args); err != nil || fs.NArg() != 3 {
		PrintUsage(osStderr, "usage: gregale realtime send APP_SLUG ENDPOINT_ID CONNECTION_ID (--data-stdin|--data DATA) [--binary]", "realtime")
		return 1
	}
	request, err := realtimeMessageRequest(*data, *dataStdin, *binary)
	if err != nil {
		return printErr("Could not read realtime message", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.SendManagedRealtimeConnection(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2), request); err != nil {
		return printErr("Could not send realtime message", err)
	}
	result := realtimeCLIResult{Operation: "send", AppSlug: fs.Arg(0), EndpointID: fs.Arg(1), ConnectionID: fs.Arg(2)}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Realtime message sent to connection %s.", fs.Arg(2))
	return 0
}

func cmdRealtimeClose(args []string) int {
	args = normalizeRealtimeCloseArgs(args)
	fs := newFlagSet("realtime close", flag.ContinueOnError)
	reason := fs.String("reason", "", "optional WebSocket close reason")
	if err := fs.Parse(args); err != nil || fs.NArg() != 3 {
		PrintUsage(osStderr, "usage: gregale realtime close APP_SLUG ENDPOINT_ID CONNECTION_ID [--reason TEXT]", "realtime")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.CloseManagedRealtimeConnection(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2), api.ManagedRealtimeCloseRequest{Reason: *reason}); err != nil {
		return printErr("Could not close realtime connection", err)
	}
	result := realtimeCLIResult{Operation: "close", AppSlug: fs.Arg(0), EndpointID: fs.Arg(1), ConnectionID: fs.Arg(2)}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Realtime connection %s closed.", fs.Arg(2))
	return 0
}

func cmdRealtimeSubscribe(args []string) int {
	return cmdRealtimeSubscription(args, true)
}

func cmdRealtimeUnsubscribe(args []string) int {
	return cmdRealtimeSubscription(args, false)
}

func cmdRealtimeSubscription(args []string, subscribe bool) int {
	if len(args) != 4 || strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" || strings.TrimSpace(args[2]) == "" || !realtime.ValidateChannel(args[3]) {
		verb := "subscribe"
		if !subscribe {
			verb = "unsubscribe"
		}
		PrintUsage(osStderr, fmt.Sprintf("usage: gregale realtime %s APP_SLUG ENDPOINT_ID CONNECTION_ID CHANNEL", verb), "realtime")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if subscribe {
		err = client.SubscribeManagedRealtimeConnection(context.Background(), args[0], args[1], args[2], args[3])
	} else {
		err = client.UnsubscribeManagedRealtimeConnection(context.Background(), args[0], args[1], args[2], args[3])
	}
	if err != nil {
		return printErr("Could not update realtime subscription", err)
	}
	verb := "subscribed"
	operation := "subscribe"
	if !subscribe {
		verb = "unsubscribed"
		operation = "unsubscribe"
	}
	result := realtimeCLIResult{Operation: operation, AppSlug: args[0], EndpointID: args[1], ConnectionID: args[2], Channel: args[3]}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Realtime connection %s %s from channel %s.", args[2], verb, args[3])
	return 0
}

func cmdRealtimePublish(args []string) int {
	args = normalizeRealtimeMessageArgs(args)
	fs := newFlagSet("realtime publish", flag.ContinueOnError)
	data := fs.String("data", "", "UTF-8 message data (prefer --data-stdin for binary-safe input)")
	dataStdin := fs.Bool("data-stdin", false, "read message bytes from stdin")
	binary := fs.Bool("binary", false, "publish as a binary WebSocket frame")
	if err := fs.Parse(args); err != nil || fs.NArg() != 3 || !realtime.ValidateChannel(fs.Arg(2)) {
		PrintUsage(osStderr, "usage: gregale realtime publish APP_SLUG ENDPOINT_ID CHANNEL (--data-stdin|--data DATA) [--binary]", "realtime")
		return 1
	}
	request, err := realtimeMessageRequest(*data, *dataStdin, *binary)
	if err != nil {
		return printErr("Could not read realtime message", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.PublishManagedRealtimeChannel(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2), request)
	if err != nil {
		return printErr("Could not publish realtime message", err)
	}
	queued := response.Queued
	result := realtimeCLIResult{Operation: "publish", AppSlug: fs.Arg(0), EndpointID: fs.Arg(1), Channel: fs.Arg(2), Queued: &queued}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	PrintOK(osStdout, "Realtime message published to %d connection(s).", response.Queued)
	return 0
}

func realtimeMessageRequest(data string, fromStdin, binary bool) (api.ManagedRealtimeMessageRequest, error) {
	if data != "" && fromStdin {
		return api.ManagedRealtimeMessageRequest{}, fmt.Errorf("--data and --data-stdin are mutually exclusive")
	}
	var payload []byte
	if fromStdin {
		body, err := io.ReadAll(io.LimitReader(osStdin, int64(api.RealtimeMessageMaxBytes)+1))
		if err != nil {
			return api.ManagedRealtimeMessageRequest{}, err
		}
		payload = body
	} else {
		if data == "" {
			return api.ManagedRealtimeMessageRequest{}, fmt.Errorf("provide --data-stdin or --data")
		}
		payload = []byte(data)
	}
	if len(payload) > api.RealtimeMessageMaxBytes {
		return api.ManagedRealtimeMessageRequest{}, fmt.Errorf("message exceeds %d bytes", api.RealtimeMessageMaxBytes)
	}
	return api.ManagedRealtimeMessageRequest{DataBase64: base64.StdEncoding.EncodeToString(payload), Binary: binary}, nil
}

func normalizeRealtimeMessageArgs(args []string) []string {
	return normalizeRealtimeValueArgs(args, map[string]bool{"--data": true}, map[string]bool{"--data-stdin": true, "--binary": true})
}

func normalizeRealtimeCloseArgs(args []string) []string {
	return normalizeRealtimeValueArgs(args, map[string]bool{"--reason": true}, nil)
}

func normalizeRealtimeValueArgs(args []string, valueFlags, boolFlags map[string]bool) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if valueFlags[arg] {
			flags = append(flags, arg)
			if i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		if boolFlags[arg] {
			flags = append(flags, arg)
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			continue
		}
		positionals = append(positionals, arg)
	}
	return append(flags, positionals...)
}
