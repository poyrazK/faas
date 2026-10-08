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
	Subscribers  *int   `json:"subscribers,omitempty"`
	QueueFull    *int   `json:"queue_full,omitempty"`
	Failed       *int   `json:"failed,omitempty"`
	Partial      *bool  `json:"partial,omitempty"`
	NodesQueried *int   `json:"nodes_queried,omitempty"`
	NodesDown    *int   `json:"nodes_unavailable,omitempty"`
	Sequence     *int64 `json:"sequence,omitempty"`
	Durable      *bool  `json:"durable,omitempty"`
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
	preposition := "to"
	if verb == "unsubscribed" {
		preposition = "from"
	}
	PrintOK(osStdout, "Realtime connection %s %s %s channel %s.", args[2], verb, preposition, args[3])
	return 0
}

func cmdRealtimePublish(args []string) int {
	args = normalizeRealtimeMessageArgs(args)
	fs := newFlagSet("realtime publish", flag.ContinueOnError)
	data := fs.String("data", "", "UTF-8 message data (prefer --data-stdin for binary-safe input)")
	dataStdin := fs.Bool("data-stdin", false, "read message bytes from stdin")
	binary := fs.Bool("binary", false, "publish as a binary WebSocket frame")
	idempotencyKey := fs.String("idempotency-key", "", "stable key for retrying this exact message")
	delivery := fs.String("delivery", "live", "delivery mode: live or retained")
	if err := fs.Parse(args); err != nil || fs.NArg() != 3 || !realtime.ValidateChannel(fs.Arg(2)) {
		PrintUsage(osStderr, "usage: gregale realtime publish APP_SLUG ENDPOINT_ID CHANNEL (--data-stdin|--data DATA) [--binary] [--delivery live|retained] [--idempotency-key KEY]", "realtime")
		return 1
	}
	if *delivery != string(api.ManagedRealtimeDeliveryLive) && *delivery != string(api.ManagedRealtimeDeliveryRetained) {
		return printErr("Could not publish realtime message", fmt.Errorf("--delivery must be live or retained"))
	}
	if *delivery == string(api.ManagedRealtimeDeliveryRetained) && *idempotencyKey == "" {
		return printErr("Could not publish realtime message", fmt.Errorf("--delivery retained requires --idempotency-key"))
	}
	request, err := realtimeMessageRequest(*data, *dataStdin, *binary)
	if err != nil {
		return printErr("Could not read realtime message", err)
	}
	if *delivery == string(api.ManagedRealtimeDeliveryRetained) {
		payload, decodeErr := base64.StdEncoding.DecodeString(request.DataBase64)
		if decodeErr != nil || len(payload) > 4<<10 {
			return printErr("Could not publish realtime message", fmt.Errorf("retained messages are limited to 4096 decoded bytes"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.PublishManagedRealtimeChannelWithDelivery(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2), request,
		api.ManagedRealtimeDelivery(*delivery), *idempotencyKey)
	if err != nil {
		return printErr("Could not publish realtime message", err)
	}
	queued, subscribers, queueFull, failed := response.Queued, response.Subscribers, response.QueueFull, response.Failed
	partial, nodesQueried, nodesDown := response.Partial, response.NodesQueried, response.NodesUnavailable
	result := realtimeCLIResult{
		Operation: "publish", AppSlug: fs.Arg(0), EndpointID: fs.Arg(1), Channel: fs.Arg(2),
		Queued: &queued, Subscribers: &subscribers, QueueFull: &queueFull, Failed: &failed,
		Partial: &partial, NodesQueried: &nodesQueried, NodesDown: &nodesDown,
	}
	if response.Durable {
		durable, sequence := response.Durable, response.Sequence
		result.Durable, result.Sequence = &durable, &sequence
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	if response.Durable {
		PrintOK(osStdout, "Realtime message retained at channel sequence %d and accepted for %d of %d targeted connection(s).", response.Sequence, response.Queued, response.Subscribers)
	} else if response.Subscribers == 0 && response.Queued > 0 {
		PrintOK(osStdout, "Realtime message queued for %d connection(s).", response.Queued)
	} else {
		PrintOK(osStdout, "Realtime message queued for %d of %d targeted connection(s).", response.Queued, response.Subscribers)
	}
	if response.Partial {
		PrintWarn(osStderr, "Publish was partial: %d queue full, %d failed, and %d realtime node(s) unavailable. Repeating without the same idempotency key may send duplicates.", response.QueueFull, response.Failed, response.NodesUnavailable)
	}
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
	return normalizeRealtimeValueArgs(args, map[string]bool{"--data": true, "--idempotency-key": true, "--delivery": true}, map[string]bool{"--data-stdin": true, "--binary": true})
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
