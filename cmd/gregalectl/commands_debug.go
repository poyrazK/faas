// commands_debug.go — ADR-127 PR-D: operator-side smoke harness
// for the OTel spans writer.
//
// Subcommands:
//
//	gregalectl debug otel-smoke   — POST a hand-crafted 3-span
//	                                ExportTraceServiceRequest to
//	                                the local gatewayd-public's
//	                                /v1/otel/v1/traces endpoint and
//	                                assert 200 + accepted_spans==3.
//	                                Used for end-to-end verification
//	                                when PR-D ships: the operator
//	                                runs the smoke, then runs a
//	                                `psql` SELECT on
//	                                request_telemetry.spans_summary
//	                                to confirm the writer landed.
//
// Wire discipline mirrors commands_obs.go: dial the gatewayd-public
// HTTP listener (NOT apid — the OTel ingest lives on the public
// listener per CLAUDE.md ownership), Bearer-token auth via the
// customer's API key, JSON output via --json for downstream tooling.
//
// URL resolution: $FAAS_GATEWAY_PUBLIC_URL wins; defaults to
// http://127.0.0.1:8080 (the gatewayd-public loopback listen
// addr on control-plane nodes; matches the convention at
// cmd/gatewayd-public/main.go::defaultListenAddr).
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	dispatchDebug = "debug"
	subDebugOtel  = "otel-smoke"

	// defaultGatewaydPublicURL is the loopback default for the
	// gatewayd-public listen addr. Matches cmd/gatewayd-public/
	// main.go::defaultListenAddr of :8080 on loopback.
	defaultGatewaydPublicURL = "http://127.0.0.1:8080"
)

// cmdDebugDispatch routes the `gregalectl debug` top-level
// command to its subcommand handlers. Mirrors the shape of
// cmdObsDispatch (commands_obs.go:65).
func cmdDebugDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl debug: missing subcommand; want otel-smoke|pprof")
		return 2
	}
	switch args[0] {
	case subDebugOtel:
		return cmdDebugOtelSmoke(args[1:])
	case subDebugPprof:
		return cmdDebugPprof(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl debug: unknown subcommand %q\nRun 'gregalectl debug otel-smoke --help' for usage.\n", args[0])
		return 2
	}
}

// gatewaydPublicBase returns the gatewayd-public base URL,
// overridable via $FAAS_GATEWAY_PUBLIC_URL for local/dev.
func gatewaydPublicBase() string {
	if v := os.Getenv("FAAS_GATEWAY_PUBLIC_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultGatewaydPublicURL
}

// buildSmokeOTLPBody assembles a minimal 3-span
// ExportTraceServiceRequest whose spans share one synthetic
// trace_id (16 bytes 0xab.. → 32-char hex). The trace_id is
// configurable via --trace-id for operators who want to drive
// the smoke against a known customer trace.
func buildSmokeOTLPBody(traceID string) []byte {
	traceIDBytes, err := hex.DecodeString(traceID)
	if err != nil || len(traceIDBytes) != 16 {
		// Default: 16 zero bytes → trace_id "00000000000000000000000000000000".
		traceIDBytes = make([]byte, 16)
	}
	spanID := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	body := map[string]any{
		"resourceSpans": []any{
			map[string]any{
				"scopeSpans": []any{
					map[string]any{
						"spans": []any{
							map[string]any{
								"traceId":           hex.EncodeToString(traceIDBytes),
								"spanId":            hex.EncodeToString(spanID),
								"name":              "smoke.span.1",
								"startTimeUnixNano": "0",
								"endTimeUnixNano":   "1000000",
							},
							map[string]any{
								"traceId":           hex.EncodeToString(traceIDBytes),
								"spanId":            hex.EncodeToString([]byte{0x02, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}),
								"name":              "smoke.span.2",
								"startTimeUnixNano": "0",
								"endTimeUnixNano":   "2000000",
							},
							map[string]any{
								"traceId":           hex.EncodeToString(traceIDBytes),
								"spanId":            hex.EncodeToString([]byte{0x03, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}),
								"name":              "smoke.span.3",
								"startTimeUnixNano": "0",
								"endTimeUnixNano":   "3000000",
							},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(body)
	return b
}

// otelSmokeResponse mirrors the handler's 200 OK body.
type otelSmokeResponse struct {
	AcceptedSpans int  `json:"accepted_spans"`
	Truncated     bool `json:"truncated"`
}

// cmdDebugOtelSmoke POSTs a 3-span ExportTraceServiceRequest to
// the gatewayd-public /v1/otel/v1/traces endpoint and asserts
// 200 + accepted_spans == 3. The endpoint requires Bearer auth
// (the customer's API key, same as every other public endpoint).
//
// Exit codes:
//
//	0 — accepted (handler returned 200 + accepted_spans == expected).
//	1 — handler returned non-200 OR accepted_spans mismatch.
//	2 — bad invocation (missing token, bad trace_id, etc).
func cmdDebugOtelSmoke(args []string) int {
	fs := flag.NewFlagSet(subDebugOtel, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout (overrides human summary)")
	token := fs.String("token", "", "Bearer token for the OTLP POST (default: $FAAS_API_KEY)")
	traceID := fs.String("trace-id", "00000000000000000000000000000000", "32-char lowercase hex trace_id for the smoke (default: all-zero)")
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout for the gatewayd-public round-trip (default 10s)")
	expectedSpans := fs.Int("expected-spans", 3, "expected accepted_spans value from the handler response (default 3)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Resolve bearer: --token wins, then $FAAS_API_KEY.
	bearer := *token
	if bearer == "" {
		bearer = os.Getenv("FAAS_API_KEY")
	}
	if bearer == "" {
		fmt.Fprintln(os.Stderr, "gregalectl debug otel-smoke: --token (or $FAAS_API_KEY) required")
		return 2
	}

	// Validate trace_id shape before sending — saves a round-trip.
	if len(*traceID) != 32 {
		fmt.Fprintf(os.Stderr, "gregalectl debug otel-smoke: --trace-id must be 32 chars (got %d)\n", len(*traceID))
		return 2
	}
	for i := 0; i < len(*traceID); i++ {
		c := (*traceID)[i]
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			fmt.Fprintf(os.Stderr, "gregalectl debug otel-smoke: --trace-id must be lowercase hex (got %q at byte %d)\n", string(c), i)
			return 2
		}
	}

	body := buildSmokeOTLPBody(*traceID)
	url := gatewaydPublicBase() + "/v1/otel/v1/traces"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl debug otel-smoke:", err)
		return 2
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: *timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl debug otel-smoke: dial gatewayd-public:", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gregalectl debug otel-smoke: read body:", err)
		return 1
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "gregalectl debug otel-smoke: gatewayd-public returned %d: %s\n", resp.StatusCode, strings.TrimSpace(string(respBody)))
		return 1
	}

	var parsed otelSmokeResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl debug otel-smoke: decode response: %v (body=%s)\n", err, strings.TrimSpace(string(respBody)))
		return 1
	}
	if parsed.AcceptedSpans != *expectedSpans {
		fmt.Fprintf(os.Stderr, "gregalectl debug otel-smoke: accepted_spans=%d, want %d\n", parsed.AcceptedSpans, *expectedSpans)
		return 1
	}

	if *jsonOut || jsonFlagEnabled(args) {
		out := map[string]any{
			"status":         "ok",
			"trace_id":       *traceID,
			"accepted_spans": parsed.AcceptedSpans,
			"truncated":      parsed.Truncated,
			"endpoint":       url,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return 0
	}
	fmt.Printf("otel-smoke: ok\n  trace_id:       %s\n  accepted_spans: %d\n  truncated:      %v\n  endpoint:       %s\n", *traceID, parsed.AcceptedSpans, parsed.Truncated, url)
	return 0
}
