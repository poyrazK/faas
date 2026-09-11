// commands_builds.go — authenticated operator build recovery.
//
// Routine sweeps go through apid so the operation is MFA-gated,
// idempotent, attributable, and traceable. Direct database repair remains a
// reviewed break-glass procedure rather than a gregalectl execution path.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

const dispatchBuilds = "builds"

var (
	buildSweepReasonShape  = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	buildSweepTraceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type buildSweepOutput struct {
	api.SweepStuckBuildsResponse
	TraceID string `json:"trace_id"`
}

func cmdBuildsDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds: missing subcommand; want sweep-stuck")
		return 2
	}
	switch args[0] {
	case "sweep-stuck":
		return cmdBuildsSweepStuck(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl builds: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdBuildsSweepStuck(args []string) int {
	fs := flag.NewFlagSet("sweep-stuck", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	olderThan := fs.Duration("older-than", 15*time.Minute, "threshold (1m..60m; default 15m)")
	reason := fs.String("reason", "", "durable audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge that matching builds will be marked failed/timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck: positional arguments are not accepted")
		return 2
	}
	cleanReason := strings.TrimSpace(*reason)
	if !buildSweepReasonShape.MatchString(cleanReason) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck: --reason is required and must match [a-z0-9_]{1,64}")
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck: --yes required (matching builds will be marked failed/timeout)")
		return 2
	}
	if *olderThan < time.Minute || *olderThan > time.Hour {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck: --older-than must be between 1m and 1h")
		return 2
	}
	traceID := strings.TrimSpace(*traceIDFlag)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !buildSweepTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck: --trace-id must be 32 lowercase hex characters")
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck:", err)
		return 1
	}
	query := url.Values{
		"confirm":    {"true"},
		"older_than": {olderThan.String()},
		"reason":     {cleanReason},
	}
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.SweepStuckBuildsResponse
	path := "/v1/admin/builds/sweep-stuck?" + query.Encode()
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), http.MethodPost,
		path, nil, &response, true, nil, headers); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl builds sweep-stuck:", err)
		return 1
	}
	output := buildSweepOutput{SweepStuckBuildsResponse: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	_, _ = fmt.Fprintf(osStdout, "swept=%d older_than=%s threshold_iso=%s trace_id=%s\n",
		response.SweptCount, time.Duration(response.OlderThanSecs)*time.Second, response.ThresholdISO, traceID)
	return 0
}
