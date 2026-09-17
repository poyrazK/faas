package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDebugRequestsInspect selects one retained request, then renders the
// complete customer-safe investigation in one pass. A request id can be
// supplied for a direct lookup; otherwise the newest row matching the
// supplied filters is selected.
func cmdDebugRequestsInspect(args []string) int {
	fs := newFlagSet("debug requests inspect", flag.ContinueOnError)
	latest := fs.Bool("latest", false, "select the newest retained request matching the filters")
	since := fs.String("since", "", "lookback window for latest-request selection (e.g. 30m, 24h, 3d)")
	route := fs.String("route", "", "route filter for latest-request selection (exact match)")
	deploymentID := fs.String("deployment-id", "", "deployment UUID filter for latest-request selection")
	status := fs.Int("status", 0, "exact HTTP status filter for latest-request selection (100..599)")
	coldBoot := fs.String("cold-boot", "", "cold-start filter for latest-request selection (true or false)")
	consumerID := fs.String("consumer-id", "", "consumer UUID or __anonymous__ for latest-request selection")
	minLatencyMS := fs.Int("min-latency-ms", 0, "minimum latency bucket for latest-request selection in milliseconds")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"latest": false, "since": true, "route": true, "deployment-id": true,
		"status": true, "cold-boot": true, "consumer-id": true, "min-latency-ms": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) < 1 || len(positional) > 2 {
		PrintUsage(os.Stderr, "usage: gregale debug requests inspect [--latest] [--since D] [--route P] [--deployment-id UUID] [--status N] [--cold-boot true|false] [--consumer-id UUID|__anonymous__] [--min-latency-ms N] <slug> [<request-id-or-row-id>]", debugCmdDocsTopic)
		return 1
	}
	if len(positional) == 2 && (*latest || debugInspectHasSelectionFilters(*since, *route, *deploymentID, *status, *coldBoot, *consumerID, *minLatencyMS)) {
		return printErr("Invalid inspect selection", fmt.Errorf("request id cannot be combined with --latest or request-selection filters"))
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	slug := positional[0]
	selection := debugRequestInspectSelection{Mode: "request_id"}
	var evidence api.DebugRequestEvidenceResponse
	if len(positional) == 2 {
		selection.RequestID = positional[1]
		evidence, err = client.GetAppDebugRequestEvidence(ctx, slug, selection.RequestID)
		if err != nil {
			return printErr("Could not inspect debug request", err)
		}
	} else {
		options, optionErr := debugTelemetryOptionsFromFlags(*since, *route, *deploymentID, *status, *coldBoot, *consumerID, *minLatencyMS, "", 1)
		if optionErr != nil {
			fmt.Fprintln(os.Stderr, optionErr)
			return 1
		}
		list, listErr := client.ListAppDebugRequestsWithOptions(ctx, slug, options)
		if listErr != nil {
			return printErr("Could not select latest debug request", listErr)
		}
		if len(list.Requests) == 0 {
			return printErr("Could not select latest debug request", fmt.Errorf("no retained request telemetry matched the supplied filters"))
		}
		selection = debugRequestInspectSelection{
			Mode:      "latest",
			RequestID: list.Requests[0].ID,
			Since:     list.Since,
			Route:     *route,
			Filters:   debugInspectFilters(*deploymentID, *status, *coldBoot, *consumerID, *minLatencyMS),
		}
		evidence, err = client.GetAppDebugRequestEvidence(ctx, slug, selection.RequestID)
		if err != nil {
			return printErr("Could not inspect selected debug request", err)
		}
	}

	output := buildDebugRequestInspectOutput(slug, evidence, selection)
	if jsonOutput {
		return jsonOut(writeJSON(output))
	}
	renderDebugRequestInspect(osStdout, slug, evidence, selection)
	return 0
}

type debugRequestInspectSelection struct {
	Mode      string                        `json:"mode"`
	RequestID string                        `json:"request_id"`
	Since     string                        `json:"since,omitempty"`
	Route     string                        `json:"route,omitempty"`
	Filters   api.DebugTelemetryListFilters `json:"filters,omitempty"`
}

type debugRequestInspectOutput struct {
	Selection   debugRequestInspectSelection     `json:"selection"`
	Evidence    api.DebugRequestEvidenceResponse `json:"evidence"`
	SpanTree    []debugRequestInspectSpanNode    `json:"span_tree"`
	NextActions []debugRequestInspectAction      `json:"next_actions"`
}

type debugRequestInspectSpanNode struct {
	Span          *api.DebugTelemetrySpan       `json:"span,omitempty"`
	Children      []debugRequestInspectSpanNode `json:"children,omitempty"`
	ParentOmitted bool                          `json:"parent_omitted,omitempty"`
	Cycle         bool                          `json:"cycle,omitempty"`
}

type debugRequestInspectAction struct {
	Name    string `json:"name"`
	Command string `json:"command"`
}

func debugInspectHasSelectionFilters(since, route, deploymentID string, status int, coldBoot, consumerID string, minLatencyMS int) bool {
	return since != "" || route != "" || deploymentID != "" || status != 0 || coldBoot != "" || consumerID != "" || minLatencyMS != 0
}

func debugInspectFilters(deploymentID string, status int, coldBoot, consumerID string, minLatencyMS int) api.DebugTelemetryListFilters {
	filters := api.DebugTelemetryListFilters{
		DeploymentID: deploymentID,
		Status:       status,
		ConsumerID:   consumerID,
		MinLatencyMS: minLatencyMS,
	}
	if coldBoot != "" {
		value := false
		if parsed, err := strconv.ParseBool(coldBoot); err == nil {
			value = parsed
		}
		filters.ColdBoot = &value
	}
	return filters
}

func buildDebugRequestInspectOutput(slug string, evidence api.DebugRequestEvidenceResponse, selection debugRequestInspectSelection) debugRequestInspectOutput {
	return debugRequestInspectOutput{
		Selection:   selection,
		Evidence:    evidence,
		SpanTree:    buildDebugRequestInspectSpanTree(buildDebugTraceRoots(evidence.Spans)),
		NextActions: debugRequestInspectActions(slug, evidence.Request.ID),
	}
}

func buildDebugRequestInspectSpanTree(roots []*debugTraceNode) []debugRequestInspectSpanNode {
	out := make([]debugRequestInspectSpanNode, 0, len(roots))
	for _, root := range roots {
		out = append(out, buildDebugRequestInspectSpanNode(root, make(map[*debugTraceNode]bool)))
	}
	return out
}

func buildDebugRequestInspectSpanNode(node *debugTraceNode, path map[*debugTraceNode]bool) debugRequestInspectSpanNode {
	if path[node] {
		return debugRequestInspectSpanNode{Cycle: true}
	}
	span := node.span
	out := debugRequestInspectSpanNode{
		Span:          &span,
		ParentOmitted: node.orphan,
		Children:      make([]debugRequestInspectSpanNode, 0, len(node.children)),
	}
	path[node] = true
	defer delete(path, node)
	for _, child := range node.children {
		out.Children = append(out.Children, buildDebugRequestInspectSpanNode(child, path))
	}
	return out
}

func debugRequestInspectActions(slug, requestID string) []debugRequestInspectAction {
	return []debugRequestInspectAction{
		{Name: "replay", Command: fmt.Sprintf("gregale debug requests replay %s %s --wait", slug, requestID)},
		{Name: "bundle", Command: fmt.Sprintf("gregale debug bundle %s %s --output debug-bundle.json", slug, requestID)},
		{Name: "compare", Command: fmt.Sprintf("gregale debug compare %s --source <source-deployment-id> --mirror <mirror-deployment-id>", slug)},
	}
}

func renderDebugRequestInspect(w io.Writer, slug string, evidence api.DebugRequestEvidenceResponse, selection debugRequestInspectSelection) {
	if selection.Mode == "latest" {
		_, _ = fmt.Fprintf(w, "Selected latest matching request: %s\n\n", selection.RequestID)
	} else {
		_, _ = fmt.Fprintf(w, "Inspecting request: %s\n\n", selection.RequestID)
	}
	renderDebugRequestEvidence(w, evidence)
	_, _ = fmt.Fprintln(w)
	if len(evidence.Spans) == 0 {
		_, _ = fmt.Fprintln(w, "span tree: no linked OTel spans")
	} else {
		_, _ = fmt.Fprintln(w, "SPAN TREE")
		roots := buildDebugTraceRoots(evidence.Spans)
		for i, root := range roots {
			renderDebugTraceNode(w, root, "", i == len(roots)-1, make(map[*debugTraceNode]bool))
		}
		if evidence.SpansTruncated {
			_, _ = fmt.Fprintln(w, "span tree truncated to the slowest retained spans")
		}
	}
	_, _ = fmt.Fprintln(w, "NEXT COMMANDS")
	for _, action := range debugRequestInspectActions(slug, evidence.Request.ID) {
		_, _ = fmt.Fprintf(w, "%-8s %s\n", action.Name+":", action.Command)
	}
}
