package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/mcphosting"
)

const (
	mcpWatchReconnectBase = 250 * time.Millisecond
	mcpWatchReconnectMax  = 5 * time.Second
	mcpWatchMaxReconnects = 3
)

type mcpWatchEvent struct {
	Event   string                  `json:"event"`
	Trigger string                  `json:"trigger"`
	Diff    mcphosting.ContractDiff `json:"diff"`
}

func cmdMCPWatch(args []string) int {
	fs := newFlagSet("mcp-watch", flag.ContinueOnError)
	endpoint := fs.String("url", "", "full MCP endpoint URL")
	app := fs.String("app", "", "Gregale app slug; resolves its public URL")
	path := fs.String("endpoint", "/mcp", "endpoint path when using --app")
	tokenEnv := fs.String("token-env", "", "environment variable containing an MCP client token")
	baselinePath := fs.String("baseline", "", "contract snapshot to watch for drift")
	interval := fs.Duration("interval", 30*time.Second, "poll interval when notification subscriptions are unavailable")
	watchTools := fs.Bool("tools", false, "watch tool definitions")
	watchResources := fs.Bool("resources", false, "watch resource and template definitions")
	watchPrompts := fs.Bool("prompts", false, "watch prompt definitions")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *baselinePath == "" || *interval <= 0 || *interval > time.Hour {
		return printErr("Invalid MCP watch flags", errors.New("--baseline and a positive --interval up to 1h are required; no positional arguments"))
	}
	filter := mcphosting.CatalogNotificationFilter{ToolsListChanged: *watchTools, ResourcesListChanged: *watchResources, PromptsListChanged: *watchPrompts}
	if !filter.ToolsListChanged && !filter.ResourcesListChanged && !filter.PromptsListChanged {
		filter = mcphosting.CatalogNotificationFilter{ToolsListChanged: true, ResourcesListChanged: true, PromptsListChanged: true}
	}
	baseline, err := readMCPContract(*baselinePath)
	if err != nil {
		return printErr("MCP watch baseline", err)
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
	currentCatalog, discovery, err := client.DiscoverCatalog(ctx)
	if err != nil {
		return printErr("MCP catalog discovery", err)
	}
	if len(discovery.RejectedTools) != 0 {
		return printErr("MCP catalog discovery", errors.New("server returned incomplete tool definitions; fix them before watching contract drift"))
	}
	current, err := mcphosting.NewCatalogContract(client.Version, currentCatalog)
	if err != nil {
		return printErr("MCP current contract", err)
	}
	if err := reportMCPWatchChange(baseline, current, "initial"); err != nil {
		return printErr("MCP watch output", err)
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStderr, "Watching MCP catalog changes for %s. Press Ctrl+C to stop.\n", mcpSafeTerminalText(resolved))
	}
	if err := runMCPWatch(ctx, client, baseline, currentCatalog, current, filter, *interval); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return 0
		}
		return printErr("MCP watch", err)
	}
	return 0
}

type mcpCatalogListenResult struct {
	change mcphosting.CatalogChange
	err    error
}

func runMCPWatch(ctx context.Context, client *mcphosting.Client, baseline mcphosting.Contract, currentCatalog mcphosting.Catalog, current mcphosting.Contract, filter mcphosting.CatalogNotificationFilter, interval time.Duration) error {
	streaming := true
	streamFailures := 0
	reconnectDelay := mcpWatchReconnectBase
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var streamCancel context.CancelFunc
	var streamResult <-chan mcpCatalogListenResult
	var retryTimer *time.Timer
	var retry <-chan time.Time
	startStream := func() {
		streamCtx, cancel := context.WithCancel(ctx)
		streamCancel = cancel
		result := make(chan mcpCatalogListenResult, 1)
		streamResult = result
		go func() {
			change, err := client.ListenCatalogChange(streamCtx, filter)
			result <- mcpCatalogListenResult{change: change, err: err}
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
			// Reconcile periodically even with SSE active: a catalog change between
			// the initial snapshot and subscription acknowledgement must not be lost.
			stopStream()
			nextCatalog, err := refreshMCPWatchCatalog(ctx, client, currentCatalog, filter, "")
			if err != nil {
				return err
			}
			currentCatalog, current, err = recordMCPWatchUpdate(baseline, current, currentCatalog, nextCatalog, "reconcile")
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
				nextCatalog, err := refreshMCPWatchCatalog(ctx, client, currentCatalog, filter, result.change)
				if err != nil {
					return err
				}
				currentCatalog, current, err = recordMCPWatchUpdate(baseline, current, currentCatalog, nextCatalog, string(result.change))
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
					_, _ = fmt.Fprintf(osStderr, "Catalog subscriptions unavailable; polling every %s.\n", interval)
				}
				continue
			}
			if !errors.Is(result.err, mcphosting.ErrSubscriptionClosed) && !errors.Is(result.err, mcphosting.ErrSubscriptionInterrupted) {
				return result.err
			}
			streamFailures++
			nextCatalog, err := refreshMCPWatchCatalog(ctx, client, currentCatalog, filter, "")
			if err != nil {
				return err
			}
			currentCatalog, current, err = recordMCPWatchUpdate(baseline, current, currentCatalog, nextCatalog, "reconnect_snapshot")
			if err != nil {
				return err
			}
			if streamFailures >= mcpWatchMaxReconnects {
				streaming = false
				if !jsonOutput {
					_, _ = fmt.Fprintf(osStderr, "Catalog stream kept disconnecting; polling every %s.\n", interval)
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

func refreshMCPWatchCatalog(ctx context.Context, client *mcphosting.Client, current mcphosting.Catalog, filter mcphosting.CatalogNotificationFilter, only mcphosting.CatalogChange) (mcphosting.Catalog, error) {
	capabilities, _, err := client.Discover(ctx)
	if err != nil {
		return mcphosting.Catalog{}, err
	}
	next := current
	next.Capabilities = updateMCPWatchCapabilityNames(current.Capabilities, capabilities, filter)
	next.Extensions = make([]string, 0, len(capabilities.Extensions))
	for name := range capabilities.Extensions {
		next.Extensions = append(next.Extensions, name)
	}
	slices.Sort(next.Extensions)
	refreshAll := only == ""
	if refreshAll || only == mcphosting.CatalogToolsListChanged {
		if filter.ToolsListChanged {
			next.Tools = make([]mcphosting.Tool, 0)
			if capabilities.Tools != nil {
				var response mcphosting.Exchange
				next.Tools, response, err = client.Tools(ctx)
				if err != nil {
					return mcphosting.Catalog{}, err
				}
				if len(response.RejectedTools) != 0 {
					return mcphosting.Catalog{}, errors.New("server returned incomplete tool definitions")
				}
			}
		}
	}
	if refreshAll || only == mcphosting.CatalogResourcesListChanged {
		if filter.ResourcesListChanged {
			next.Resources = make([]mcphosting.Resource, 0)
			next.ResourceTemplates = make([]mcphosting.ResourceTemplate, 0)
			if capabilities.Resources != nil {
				next.Resources, err = client.Resources(ctx)
				if err != nil {
					return mcphosting.Catalog{}, err
				}
				next.ResourceTemplates, err = client.ResourceTemplates(ctx)
				if err != nil {
					return mcphosting.Catalog{}, err
				}
			}
		}
	}
	if refreshAll || only == mcphosting.CatalogPromptsListChanged {
		if filter.PromptsListChanged {
			next.Prompts = make([]mcphosting.Prompt, 0)
			if capabilities.Prompts != nil {
				next.Prompts, _, err = client.Prompts(ctx)
				if err != nil {
					return mcphosting.Catalog{}, err
				}
			}
		}
	}
	return next, nil
}

func updateMCPWatchCapabilityNames(current []string, capabilities mcphosting.ServerCapabilities, filter mcphosting.CatalogNotificationFilter) []string {
	selected := make(map[string]bool, len(current)+3)
	for _, name := range current {
		selected[name] = true
	}
	for name, watch := range map[string]bool{"tools": filter.ToolsListChanged, "resources": filter.ResourcesListChanged, "prompts": filter.PromptsListChanged} {
		if !watch {
			continue
		}
		var advertised bool
		switch name {
		case "tools":
			advertised = capabilities.Tools != nil
		case "resources":
			advertised = capabilities.Resources != nil
		case "prompts":
			advertised = capabilities.Prompts != nil
		}
		if advertised {
			selected[name] = true
		} else {
			delete(selected, name)
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func recordMCPWatchUpdate(baseline, current mcphosting.Contract, currentCatalog, nextCatalog mcphosting.Catalog, trigger string) (mcphosting.Catalog, mcphosting.Contract, error) {
	next, err := mcphosting.NewCatalogContract(current.ProtocolVersion, nextCatalog)
	if err != nil {
		return currentCatalog, current, err
	}
	currentBytes, err := mcphosting.MarshalContract(current)
	if err != nil {
		return currentCatalog, current, err
	}
	nextBytes, err := mcphosting.MarshalContract(next)
	if err != nil {
		return currentCatalog, current, err
	}
	if bytes.Equal(currentBytes, nextBytes) {
		return nextCatalog, next, nil
	}
	currentDiff, err := mcphosting.CompareContracts(baseline, current)
	if err != nil {
		return currentCatalog, current, err
	}
	diff, err := mcphosting.CompareContracts(baseline, next)
	if err != nil {
		return currentCatalog, current, err
	}
	if !reflect.DeepEqual(currentDiff, diff) {
		eventName := "catalog_drift"
		if len(diff.Changes) == 0 && len(currentDiff.Changes) > 0 {
			eventName = "catalog_drift_cleared"
		}
		if err := writeMCPWatchEvent(mcpWatchEvent{Event: eventName, Trigger: trigger, Diff: diff}); err != nil {
			return currentCatalog, current, err
		}
	}
	return nextCatalog, next, nil
}

func reportMCPWatchChange(baseline, current mcphosting.Contract, trigger string) error {
	diff, err := mcphosting.CompareContracts(baseline, current)
	if err != nil {
		return err
	}
	if len(diff.Changes) == 0 {
		return nil
	}
	return writeMCPWatchEvent(mcpWatchEvent{Event: "catalog_drift", Trigger: trigger, Diff: diff})
}

func writeMCPWatchEvent(event mcpWatchEvent) error {
	if jsonOutput {
		return json.NewEncoder(osStdout).Encode(event)
	}
	_, err := fmt.Fprintf(osStdout, "MCP catalog drift (%s): compatible=%t breaking=%t needs_review=%t\n", event.Trigger, event.Diff.Compatible, event.Diff.Breaking, event.Diff.NeedsReview)
	if err != nil {
		return err
	}
	for _, change := range event.Diff.Changes {
		var subject string
		switch {
		case change.Tool != "":
			subject = "tool " + mcpSafeTerminalText(change.Tool)
		case change.Resource != "":
			subject = "resource " + mcpSafeTerminalText(change.Resource)
		case change.ResourceTemplate != "":
			subject = "resource template " + mcpSafeTerminalText(change.ResourceTemplate)
		case change.Prompt != "":
			subject = "prompt " + mcpSafeTerminalText(change.Prompt)
		case change.Capability != "":
			subject = "capability " + mcpSafeTerminalText(change.Capability)
		default:
			subject = "catalog"
		}
		if _, err := fmt.Fprintf(osStdout, "  %s %s %s (%s)\n", mcpSafeTerminalText(change.Kind), subject, mcpSafeTerminalText(change.Path), mcpSafeTerminalText(change.Severity)); err != nil {
			return err
		}
	}
	return nil
}
