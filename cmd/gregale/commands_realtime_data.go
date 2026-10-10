package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/realtime"
)

// realtimeCLIResult is the stable, non-payload result returned by mutating
// realtime CLI operations. Message bytes are deliberately never echoed.
type realtimeCLIResult struct {
	FallbackDeadline string `json:"fallback_deadline,omitempty"`
	Operation        string `json:"operation"`
	AppSlug          string `json:"app_slug"`
	EndpointID       string `json:"endpoint_id"`
	ConnectionID     string `json:"connection_id,omitempty"`
	MessageID        string `json:"message_id,omitempty"`
	ReceiptStatus    string `json:"receipt_status,omitempty"`
	Acknowledged     *int   `json:"acknowledged,omitempty"`
	Pending          *int   `json:"pending,omitempty"`
	TimedOut         *int   `json:"timed_out,omitempty"`
	Receipt          *bool  `json:"receipt_requested,omitempty"`
	Recipients       *int   `json:"recipients,omitempty"`
	Channel          string `json:"channel,omitempty"`
	Queued           *int   `json:"queued,omitempty"`
	Subscribers      *int   `json:"subscribers,omitempty"`
	QueueFull        *int   `json:"queue_full,omitempty"`
	Failed           *int   `json:"failed,omitempty"`
	Partial          *bool  `json:"partial,omitempty"`
	NodesQueried     *int   `json:"nodes_queried,omitempty"`
	NodesDown        *int   `json:"nodes_unavailable,omitempty"`
	Unsupported      *int   `json:"unsupported,omitempty"`
	Sequence         *int64 `json:"sequence,omitempty"`
	Durable          *bool  `json:"durable,omitempty"`
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

func cmdRealtimeSendPrincipal(args []string) int {
	args = normalizeRealtimePrincipalMessageArgs(args)
	fs := newFlagSet("realtime send-principal", flag.ContinueOnError)
	principal := fs.String("principal", "", "verified OIDC principal to target")
	messageID := fs.String("message-id", "", "stable message ID for deduplication; required with --receipt or --delivery retained")
	receipt := fs.Bool("receipt", false, "track per-connection client acknowledgements for one hour")
	delivery := fs.String("delivery", "live", "live or retained (durable principal inbox)")
	notBefore := fs.String("notification-not-before", "", "earliest push delivery, RFC3339 with timezone; up to 48 hours ahead")
	collapse := fs.String("notification-collapse-key", "", "replace queued alerts sharing this key; retained fallback only")
	ttl := fs.Int("notification-ttl-seconds", 0, "push lifetime from publication, up to 259200 seconds; retained fallback only")
	priority := fs.String("notification-priority", "", "low, normal (default), or urgent; retained fallback only")
	groupKey := fs.String("notification-group", "", "stable conversation, job or project group key")
	groupLabel := fs.String("notification-group-label", "", "human-readable group name for summaries")
	category := fs.String("notification-category", "", "push preference category; defaults to notifications")
	fallbackAfter := fs.Int("fallback-after-seconds", 0, "request push/webhook fallback after 1..86400 seconds without a device acknowledgement; retained only")
	data := fs.String("data", "", "UTF-8 message data (prefer --data-stdin for binary-safe input)")
	dataStdin := fs.Bool("data-stdin", false, "read message bytes from stdin")
	binary := fs.Bool("binary", false, "send the message as a binary WebSocket frame")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		PrintUsage(osStderr, "usage: gregale realtime send-principal APP_SLUG ENDPOINT_ID --principal ID (--data-stdin|--data DATA) [--binary] [--message-id ID --receipt|--delivery retained --message-id ID] [--fallback-after-seconds N --notification-category NAME --notification-group KEY --notification-group-label LABEL --notification-priority low|normal|urgent --notification-ttl-seconds N --notification-collapse-key KEY --notification-not-before RFC3339]", "realtime")
		return 1
	}
	if *delivery != "live" && *delivery != "retained" {
		return printErr("Invalid realtime delivery", fmt.Errorf("--delivery must be live or retained"))
	}
	if _, err := api.ParseRealtimeNotificationNotBefore(*notBefore, time.Now().UTC()); err != nil || *notBefore != "" && *fallbackAfter == 0 {
		return printErr("Invalid notification schedule", fmt.Errorf("requires fallback and an RFC3339 timestamp at most 48 hours ahead"))
	}
	if api.ValidateRealtimeNotificationGroup(*collapse, "") != nil || *collapse != "" && *fallbackAfter == 0 {
		return printErr("Invalid notification collapse key", fmt.Errorf("requires fallback and a valid bounded key"))
	}
	if *ttl < 0 || *ttl > 259200 || *ttl > 0 && (*fallbackAfter == 0 || *ttl <= *fallbackAfter) {
		return printErr("Invalid notification TTL", fmt.Errorf("must exceed fallback deadline and be at most 259200 seconds"))
	}
	if api.ValidateRealtimeNotificationPriority(*priority) != nil || (*priority != "" && *fallbackAfter == 0) {
		return printErr("Invalid notification priority", fmt.Errorf("requires a fallback and must be low, normal or urgent"))
	}
	if api.ValidateRealtimeNotificationGroup(*groupKey, *groupLabel) != nil || (*groupKey != "" && *fallbackAfter == 0) {
		return printErr("Invalid notification group", fmt.Errorf("requires a fallback and valid group metadata"))
	}
	if *category != "" && (*fallbackAfter == 0 || api.ValidateRealtimeNotificationCategory(*category) != nil) {
		return printErr("Invalid notification category", fmt.Errorf("requires a fallback and 1..64 lowercase letters, digits, dots, underscores or hyphens"))
	}
	if *fallbackAfter < 0 || *fallbackAfter > 86400 || (*fallbackAfter > 0 && *delivery != "retained") {
		return printErr("Invalid fallback deadline", fmt.Errorf("--fallback-after-seconds requires --delivery retained and must be 1..86400, or zero to disable"))
	}
	if *receipt && *delivery == "retained" {
		return printErr("Invalid realtime receipt request", fmt.Errorf("--receipt only applies to live sends; retained inboxes use device checkpoints"))
	}
	if (*receipt || *delivery == "retained") && *messageID == "" {
		return printErr("Invalid realtime receipt request", fmt.Errorf("--message-id is required with --receipt or --delivery retained so retries use the same ID"))
	}
	if *messageID != "" && !*receipt && *delivery != "retained" {
		return printErr("Invalid realtime message ID", fmt.Errorf("--message-id requires --receipt or --delivery retained"))
	}
	if *messageID != "" {
		if err := api.ValidateRealtimeDirectMessageID(*messageID); err != nil {
			return printErr("Invalid realtime message ID", err)
		}
	}
	if err := api.ValidateRealtimePrincipal(*principal); err != nil {
		return printErr("Invalid realtime principal", err)
	}
	request, err := realtimeMessageRequest(*data, *dataStdin, *binary)
	if err != nil {
		return printErr("Could not read realtime message", err)
	}
	payload, err := base64.StdEncoding.DecodeString(request.DataBase64)
	if err != nil || len(payload) > api.RealtimePrincipalMessageMaxBytes {
		return printErr("Could not send realtime message", fmt.Errorf("principal messages are limited to %d bytes", api.RealtimePrincipalMessageMaxBytes))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	deliveryMode := api.ManagedRealtimeDelivery(*delivery)
	if *delivery == "live" {
		deliveryMode = ""
	}
	response, err := client.SendManagedRealtimePrincipal(context.Background(), fs.Arg(0), fs.Arg(1), api.ManagedRealtimePrincipalMessageRequest{
		Principal: *principal, DataBase64: request.DataBase64, Binary: request.Binary,
		NotificationNotBefore: *notBefore, NotificationCollapseKey: *collapse, NotificationTTLSeconds: *ttl, NotificationPriority: *priority, NotificationGroupKey: *groupKey, NotificationGroupLabel: *groupLabel, NotificationCategory: *category, MessageID: *messageID, RequestReceipt: *receipt, Delivery: deliveryMode, FallbackAfterSeconds: *fallbackAfter,
	})
	if err != nil {
		return printErr("Could not send realtime message to principal", err)
	}
	recipients, queued := response.Recipients, response.Queued
	queueFull, failed, unsupported := response.QueueFull, response.Failed, response.Unsupported
	partial, nodesQueried, nodesDown := response.Partial, response.NodesQueried, response.NodesUnavailable
	acknowledged, pending, timedOut := response.Acknowledged, response.Pending, response.TimedOut
	receiptRequested := response.ReceiptRequested
	sequence, durable := response.Sequence, response.Durable
	result := realtimeCLIResult{
		Operation: "send-principal", AppSlug: fs.Arg(0), EndpointID: fs.Arg(1),
		FallbackDeadline: response.FallbackDeadline, MessageID: response.MessageID, ReceiptStatus: response.ReceiptStatus, Sequence: &sequence, Durable: &durable,
		Recipients: &recipients, Queued: &queued, QueueFull: &queueFull, Failed: &failed,
		Unsupported: &unsupported, Partial: &partial, NodesQueried: &nodesQueried, NodesDown: &nodesDown,
		Acknowledged: &acknowledged, Pending: &pending, TimedOut: &timedOut, Receipt: &receiptRequested,
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	if response.Durable {
		PrintOK(osStdout, "Realtime inbox message %s retained at sequence %d.", response.MessageID, response.Sequence)
		if response.FallbackDeadline != "" {
			PrintOK(osStdout, "Fallback deadline: %s (cancelled by any device acknowledgement).", response.FallbackDeadline)
		}
		return 0
	}
	if response.MessageID != "" {
		PrintOK(osStdout, "Realtime message %s queued for %d of %d active connection(s).", response.MessageID, queued, recipients)
	} else {
		PrintOK(osStdout, "Realtime message queued for %d of %d active connection(s).", queued, recipients)
	}
	if partial {
		PrintWarn(osStderr, "Delivery was partial: %d connection(s) without receipt support, %d queue full, %d failed, and %d realtime node(s) unavailable.", unsupported, queueFull, failed, nodesDown)
	}
	if receiptRequested {
		PrintOK(osStdout, "Receipt status: %s (%d acknowledged, %d pending, %d timed out).", response.ReceiptStatus, acknowledged, pending, timedOut)
		PrintOK(osStdout, "Inspect it with: gregale realtime receipt %s %s %s", fs.Arg(0), fs.Arg(1), response.MessageID)
	}
	return 0
}

func cmdRealtimePrincipalReceipt(args []string) int {
	fs := newFlagSet("realtime receipt", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 3 {
		PrintUsage(osStderr, "usage: gregale realtime receipt APP_SLUG ENDPOINT_ID MESSAGE_ID", "realtime")
		return 1
	}
	if err := api.ValidateRealtimeDirectMessageID(fs.Arg(2)); err != nil {
		return printErr("Invalid realtime message ID", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	receipt, err := client.GetManagedRealtimePrincipalReceipt(context.Background(), fs.Arg(0), fs.Arg(1), fs.Arg(2))
	if err != nil {
		return printErr("Could not read realtime receipt", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	PrintOK(osStdout, "Receipt %s: %s; %d acknowledged, %d pending, %d timed out, %d unsupported.", receipt.MessageID, receipt.Status, receipt.Acknowledged, receipt.Pending, receipt.TimedOut, receipt.Unsupported)
	for _, delivery := range receipt.Deliveries {
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s\n", delivery.ConnectionID, delivery.Status, delivery.CreatedAt)
	}
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

func normalizeRealtimePrincipalMessageArgs(args []string) []string {
	return normalizeRealtimeValueArgs(args, map[string]bool{"--principal": true, "--message-id": true, "--data": true, "--delivery": true, "--fallback-after-seconds": true, "--notification-category": true, "--notification-not-before": true, "--notification-collapse-key": true, "--notification-ttl-seconds": true, "--notification-priority": true, "--notification-group": true, "--notification-group-label": true}, map[string]bool{"--data-stdin": true, "--binary": true, "--receipt": true})
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

func cmdRealtimeInbox(args []string) int {
	args = normalizeRealtimeValueArgs(args, map[string]bool{"--principal": true, "--consumer": true, "--after": true, "--limit": true}, nil)
	fs := newFlagSet("realtime inbox", flag.ContinueOnError)
	principal := fs.String("principal", "", "verified OIDC principal to inspect")
	consumer := fs.String("consumer", "", "optional device checkpoint to inspect")
	after := fs.Int64("after", -1, "read after this sequence; defaults to the device checkpoint or zero")
	limit := fs.Int("limit", 100, "maximum messages to read (1-100)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		PrintUsage(osStderr, "usage: gregale realtime inbox APP_SLUG ENDPOINT_ID --principal ID [--consumer DEVICE] [--after SEQUENCE] [--limit N]", "realtime")
		return 1
	}
	if err := api.ValidateRealtimePrincipal(*principal); err != nil {
		return printErr("Invalid inbox principal", err)
	}
	if *after < -1 || *limit < 1 || *limit > 100 {
		return printErr("Invalid inbox query", fmt.Errorf("--after must be non-negative and --limit must be between 1 and 100"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	response, err := client.ReadManagedRealtimeInbox(context.Background(), fs.Arg(0), fs.Arg(1), *principal, *consumer, *after, *limit)
	if err != nil {
		return printErr("Could not read realtime inbox", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(response))
	}
	PrintOK(osStdout, "Inbox sequences %d through %d; %d message(s) in this page.", response.OldestSequence, response.LatestSequence, len(response.Messages))
	if response.AcknowledgedSequence != nil {
		PrintOK(osStdout, "Device %s acknowledged through sequence %d.", response.Consumer, *response.AcknowledgedSequence)
	}
	if response.HistoryUnavailable {
		PrintWarn(osStderr, "This cursor has expired; rebuild application state before resetting the device cursor.")
	}
	for _, message := range response.Messages {
		_, _ = fmt.Fprintf(osStdout, "%d  %s  expires %s\n", message.Sequence, message.MessageID, message.ExpiresAt)
	}
	return 0
}
