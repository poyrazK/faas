package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdDebugRequestsTrace renders only the customer-safe OTel portion of a
// request investigation. The evidence endpoint remains the single server
// read, so this command shares its retention, tenant, MFA, and redaction
// guarantees with `requests show` without exposing the operator-only trace
// ring endpoint.
func cmdDebugRequestsTrace(args []string) int {
	if len(args) != 2 {
		PrintUsage(os.Stderr, "usage: gregale debug requests trace <slug> <request-id-or-row-id>", debugCmdDocsTopic)
		return 1
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppDebugRequestEvidence(context.Background(), args[0], args[1])
	if err != nil {
		return printErr("Could not get debug request trace", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(debugRequestTraceOutput{
			Request:        resp.Request,
			TraceID:        debugRequestTraceID(resp.Request),
			Spans:          resp.Spans,
			SpansTruncated: resp.SpansTruncated,
		}))
	}
	renderDebugRequestTrace(osStdout, resp)
	return 0
}

type debugRequestTraceOutput struct {
	Request        api.DebugTelemetryRequestItem `json:"request"`
	TraceID        string                        `json:"trace_id,omitempty"`
	Spans          []api.DebugTelemetrySpan      `json:"spans"`
	SpansTruncated bool                          `json:"spans_truncated"`
}

func debugRequestTraceID(request api.DebugTelemetryRequestItem) string {
	if request.TraceID == nil {
		return ""
	}
	return *request.TraceID
}

// debugTraceNode is intentionally built from the already-sanitized API span
// shape. Parent IDs are used only to improve presentation; a missing parent
// (for example, because the server retained only the slowest spans) makes the
// span an explicit root instead of being silently discarded.
type debugTraceNode struct {
	span     api.DebugTelemetrySpan
	children []*debugTraceNode
	orphan   bool
}

func buildDebugTraceRoots(spans []api.DebugTelemetrySpan) []*debugTraceNode {
	nodes := make(map[string]*debugTraceNode, len(spans))
	ordered := make([]*debugTraceNode, 0, len(spans))
	for i := range spans {
		span := spans[i]
		key := span.SpanID
		if key == "" {
			key = fmt.Sprintf("__span_%d", i)
		}
		node := &debugTraceNode{span: span}
		// A valid OTLP trace cannot contain duplicate span IDs. Keep both
		// rows visible if malformed or future input does, while retaining
		// the first node as the canonical parent target.
		if _, exists := nodes[key]; exists {
			key = fmt.Sprintf("%s#%d", key, i)
		}
		nodes[key] = node
		ordered = append(ordered, node)
	}

	roots := make([]*debugTraceNode, 0, len(ordered))
	for _, node := range ordered {
		parentID := node.span.ParentSpanID
		if parentID == "" {
			roots = append(roots, node)
			continue
		}
		parent, ok := nodes[parentID]
		if !ok {
			// The map can use a synthetic key for an empty/duplicate ID,
			// but parent IDs always refer to the original ID. Unknown
			// parents are represented as orphan roots.
			node.orphan = true
			roots = append(roots, node)
			continue
		}
		if parent == node {
			node.orphan = true
			roots = append(roots, node)
			continue
		}
		parent.children = append(parent.children, node)
	}
	// Malformed input can contain a parent cycle, leaving no natural root.
	// Promote one node from every unreachable component so the CLI still
	// shows all retained evidence and the renderer can mark the cycle.
	reachable := make(map[*debugTraceNode]bool, len(ordered))
	var markReachable func(*debugTraceNode)
	markReachable = func(node *debugTraceNode) {
		if reachable[node] {
			return
		}
		reachable[node] = true
		for _, child := range node.children {
			markReachable(child)
		}
	}
	for _, root := range roots {
		markReachable(root)
	}
	for _, node := range ordered {
		if !reachable[node] {
			node.orphan = true
			roots = append(roots, node)
			markReachable(node)
		}
	}

	var sortNodes func([]*debugTraceNode)
	sortNodes = func(nodes []*debugTraceNode) {
		sort.SliceStable(nodes, func(i, j int) bool {
			if nodes[i].span.DurationNanos != nodes[j].span.DurationNanos {
				return nodes[i].span.DurationNanos > nodes[j].span.DurationNanos
			}
			if nodes[i].span.Name != nodes[j].span.Name {
				return nodes[i].span.Name < nodes[j].span.Name
			}
			return nodes[i].span.SpanID < nodes[j].span.SpanID
		})
		for _, node := range nodes {
			sortNodes(node.children)
		}
	}
	sortNodes(roots)
	return roots
}

func renderDebugRequestTrace(w io.Writer, resp api.DebugRequestEvidenceResponse) {
	request := resp.Request
	_, _ = fmt.Fprintf(w, "%s %s · HTTP %d · %d ms\n", request.Method, request.Route, request.Status, request.LatencyMS)
	_, _ = fmt.Fprintf(w, "telemetry row %s", request.ID)
	if traceID := debugRequestTraceID(request); traceID != "" {
		_, _ = fmt.Fprintf(w, " · public request %s", traceID)
	}
	_, _ = fmt.Fprintln(w)

	if len(resp.Spans) == 0 {
		_, _ = fmt.Fprintln(w, "span tree: no linked OTel spans")
		return
	}

	_, _ = fmt.Fprintln(w, "SPAN TREE")
	roots := buildDebugTraceRoots(resp.Spans)
	for i, root := range roots {
		renderDebugTraceNode(w, root, "", i == len(roots)-1, make(map[*debugTraceNode]bool))
	}
	if resp.SpansTruncated {
		_, _ = fmt.Fprintln(w, "span tree truncated to the slowest retained spans")
	}
}

func renderDebugTraceNode(w io.Writer, node *debugTraceNode, prefix string, last bool, path map[*debugTraceNode]bool) {
	connector := "├─ "
	childPrefix := prefix + "│  "
	if last {
		connector = "└─ "
		childPrefix = prefix + "   "
	}
	if path[node] {
		_, _ = fmt.Fprintf(w, "%s%s<span cycle>\n", prefix, connector)
		return
	}
	path[node] = true
	defer delete(path, node)

	label := node.span.Name
	if label == "" {
		label = "<unnamed span>"
	}
	kind := node.span.Kind
	if kind == "" {
		kind = "unknown"
	}
	label = fmt.Sprintf("%s [%s] %s", label, kind, formatDebugSpanDuration(node.span.DurationNanos))
	if node.span.Status != "" {
		label += " · status=" + node.span.Status
	}
	if node.span.DBStatement != "" {
		label += " · db=" + node.span.DBStatement
	}
	if node.orphan {
		label += " · parent omitted"
	}
	_, _ = fmt.Fprintf(w, "%s%s%s\n", prefix, connector, label)
	for i, child := range node.children {
		renderDebugTraceNode(w, child, childPrefix, i == len(node.children)-1, path)
	}
}

func formatDebugSpanDuration(durationNanos uint64) string {
	ms := float64(durationNanos) / 1_000_000
	if ms >= 100 {
		return fmt.Sprintf("%.0f ms", ms)
	}
	if ms >= 1 {
		return fmt.Sprintf("%.2f ms", ms)
	}
	return fmt.Sprintf("%.3f ms", ms)
}
