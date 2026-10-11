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
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

type mcpResourceWatchResult struct {
	uri string
	err error
}

type mcpResourceWatchEvent struct {
	Event   string          `json:"event"`
	Trigger string          `json:"trigger"`
	URI     string          `json:"uri"`
	Result  json.RawMessage `json:"result"`
}

func cmdMCPResourceWatch(args []string) int {
	fs := newFlagSet("mcp-resource-watch", flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug; resolves its public URL")
	path := fs.String("endpoint", "/mcp", "endpoint path with --app (default /mcp)")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP client token")
	uri := fs.String("uri", "", "resource URI to read and watch")
	interval := fs.Duration("interval", 30*time.Second, "poll and stream reconciliation interval")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *uri == "" || *interval <= 0 || *interval > time.Hour {
		return printErr("Invalid MCP resource-watch flags", errors.New("--uri and a positive --interval up to 1h are required; no positional arguments"))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	resolved, err := mcpEndpoint(ctx, *endpoint, *app, *path)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	token, err := mcpToken(*tokenEnv)
	if err != nil {
		return printErr("MCP client token", err)
	}
	client, err := mcphosting.NewClient(resolved, token, mcphosting.ProtocolVersion)
	if err != nil {
		return printErr("MCP endpoint", err)
	}
	initial, err := client.ReadResource(ctx, *uri)
	if err != nil {
		return printErr("MCP resource read", err)
	}
	if err := writeMCPResourceWatchEvent(mcpResourceWatchEvent{Event: "snapshot", Trigger: "initial", URI: *uri, Result: initial.Result}); err != nil {
		return printErr("MCP resource-watch output", err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Watching MCP resource %s. Press Ctrl+C to stop.\n", mcpSafeTerminalText(*uri))
	}
	if err := runMCPResourceWatch(ctx, client, *uri, initial.Result, *interval); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return 0
		}
		return printErr("MCP resource watch", err)
	}
	return 0
}

func runMCPResourceWatch(ctx context.Context, client *mcphosting.Client, uri string, current json.RawMessage, interval time.Duration) error {
	streaming := true
	streamFailures := 0
	reconnectDelay := mcpWatchReconnectBase
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var streamCancel context.CancelFunc
	var streamResult <-chan mcpResourceWatchResult
	var retryTimer *time.Timer
	var retry <-chan time.Time
	startStream := func() {
		streamCtx, cancel := context.WithCancel(ctx)
		streamCancel = cancel
		result := make(chan mcpResourceWatchResult, 1)
		streamResult = result
		go func() {
			updatedURI, err := client.ListenResourceUpdated(streamCtx, uri)
			result <- mcpResourceWatchResult{uri: updatedURI, err: err}
		}()
	}
	stopStream := func() {
		if streamCancel == nil {
			return
		}
		streamCancel()
		<-streamResult
		streamCancel = nil
		streamResult = nil
	}
	startStream()
	defer func() {
		stopStream()
		if retryTimer != nil {
			retryTimer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			stopStream()
			next, err := client.ReadResource(ctx, uri)
			if err != nil {
				return err
			}
			current, err = recordMCPResourceWatchUpdate(current, next.Result, uri, "reconcile")
			if err != nil {
				return err
			}
			if streaming && retry == nil {
				startStream()
			}
		case <-retry:
			retry = nil
			retryTimer = nil
			if streaming {
				startStream()
			}
		case result := <-streamResult:
			streamCancel()
			streamCancel = nil
			streamResult = nil
			if result.err == nil {
				streamFailures = 0
				reconnectDelay = mcpWatchReconnectBase
				next, err := client.ReadResource(ctx, uri)
				if err != nil {
					return err
				}
				current, err = recordMCPResourceWatchUpdate(current, next.Result, uri, "notification")
				if err != nil {
					return err
				}
				startStream()
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(result.err, mcphosting.ErrSubscriptionsUnsupported) {
				streaming = false
				if !jsonOutput {
					_, _ = fmt.Fprintf(osStderr, "Resource subscriptions unavailable; polling every %s.\n", interval)
				}
				continue
			}
			if !errors.Is(result.err, mcphosting.ErrSubscriptionClosed) && !errors.Is(result.err, mcphosting.ErrSubscriptionInterrupted) {
				return result.err
			}
			streamFailures++
			next, err := client.ReadResource(ctx, uri)
			if err != nil {
				return err
			}
			current, err = recordMCPResourceWatchUpdate(current, next.Result, uri, "reconnect_snapshot")
			if err != nil {
				return err
			}
			if streamFailures >= mcpWatchMaxReconnects {
				streaming = false
				if !jsonOutput {
					_, _ = fmt.Fprintf(osStderr, "Resource stream kept disconnecting; polling every %s.\n", interval)
				}
				continue
			}
			if retryTimer != nil {
				retryTimer.Stop()
			}
			retryTimer = time.NewTimer(reconnectDelay)
			retry = retryTimer.C
			reconnectDelay *= 2
			if reconnectDelay > mcpWatchReconnectMax {
				reconnectDelay = mcpWatchReconnectMax
			}
		}
	}
}

func recordMCPResourceWatchUpdate(current, next json.RawMessage, uri, trigger string) (json.RawMessage, error) {
	currentValue, err := canonicalMCPResourceResult(current)
	if err != nil {
		return current, err
	}
	nextValue, err := canonicalMCPResourceResult(next)
	if err != nil {
		return current, err
	}
	if bytes.Equal(currentValue, nextValue) {
		return next, nil
	}
	if err := writeMCPResourceWatchEvent(mcpResourceWatchEvent{Event: "resource_updated", Trigger: trigger, URI: uri, Result: next}); err != nil {
		return current, err
	}
	return next, nil
}

func canonicalMCPResourceResult(raw json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode MCP resource result: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("MCP resource result must contain one JSON value")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode MCP resource result: %w", err)
	}
	return encoded, nil
}

func writeMCPResourceWatchEvent(event mcpResourceWatchEvent) error {
	if jsonOutput {
		return json.NewEncoder(osStdout).Encode(event)
	}
	encoded, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("encode MCP resource-watch output: %w", err)
	}
	_, err = fmt.Fprintln(osStdout, string(encoded))
	return err
}
