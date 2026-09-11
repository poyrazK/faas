// commands_github.go — authenticated GitHub delivery recovery.
//
// Routine reads and retries go through apid and githubd. The CLI never opens a
// PostgreSQL connection or receives customer webhook payloads.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

const dispatchGithub = "github"

var (
	githubRecoveryReasonShape  = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	githubRecoveryTraceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type githubRecoveryRetryOutput struct {
	api.GithubRecoveryRetryResponse
	TraceID string `json:"trace_id"`
}

func cmdGithubDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl github: missing subcommand; want status|retry-delivery|retry-check")
		return 2
	}
	switch args[0] {
	case "status":
		return cmdGithubStatus(args[1:])
	case "retry-delivery", "retry-check":
		return cmdGithubRetry(args[0], args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl github: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdGithubStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	status := fs.String("status", "dead", "queue status: pending|processing|succeeded|dead (empty lists all)")
	limit := fs.Int("limit", 100, "maximum rows per queue (1..500)")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > 500 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl github status: no positional args; --limit must be 1..500")
		return 2
	}
	if !validGithubCLIStatus(*status) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl github status: --status must be pending|processing|succeeded|dead (or empty)")
		return 2
	}
	query := url.Values{"status": {*status}, "limit": {strconv.Itoa(*limit)}}
	var response api.GithubRecoveryStatusResponse
	if err := githubRecoveryRequest(http.MethodGet, "/v1/admin/ops/github/recovery?"+query.Encode(), nil, &response, false, nil); err != nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl github status:", err)
		return 1
	}
	if *jsonOut || jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printGithubRecoveryStatus(response, *status)
	return 0
}

func cmdGithubRetry(action string, args []string) int {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	targetFlag := "delivery-id"
	if action == "retry-check" {
		targetFlag = "deployment-id"
	}
	var targetID string
	fs.StringVar(&targetID, targetFlag, "", "UUID of the dead recovery item")
	reason := fs.String("reason", "", "durable audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	yes := fs.Bool("yes", false, "acknowledge retrying customer GitHub work")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cleanID, cleanReason, traceID, ok := validateGithubRetry(fs, action, targetFlag, targetID, *reason, *traceIDFlag, *yes)
	if !ok {
		return 2
	}
	kind, pathSegment := "delivery", "deliveries"
	if action == "retry-check" {
		kind, pathSegment = "check_update", "check-updates"
	}
	path := "/v1/admin/ops/github/" + pathSegment + "/" + url.PathEscape(cleanID) + "/retry?confirm=true&reason=" + url.QueryEscape(cleanReason)
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.GithubRecoveryRetryResponse
	if err := githubRecoveryRequest(http.MethodPost, path, nil, &response, true, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl github %s: %v\n", action, err)
		return 1
	}
	output := githubRecoveryRetryOutput{GithubRecoveryRetryResponse: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	_, _ = fmt.Fprintf(osStdout, "retried %s=%s status=%s trace_id=%s\n", kind, cleanID, response.Status, traceID)
	return 0
}

func validateGithubRetry(fs *flag.FlagSet, action, targetFlag, targetID, reason, traceID string, yes bool) (string, string, string, bool) {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl github %s: positional arguments are not accepted\n", action)
		return "", "", "", false
	}
	targetID = strings.TrimSpace(targetID)
	if _, err := uuid.Parse(targetID); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl github %s: --%s is required and must be a UUID\n", action, targetFlag)
		return "", "", "", false
	}
	reason = strings.TrimSpace(reason)
	if !githubRecoveryReasonShape.MatchString(reason) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl github %s: --reason is required and must match [a-z0-9_]{1,64}\n", action)
		return "", "", "", false
	}
	if !yes {
		_, _ = fmt.Fprintf(osStderr, "gregalectl github %s: --yes required\n", action)
		return "", "", "", false
	}
	traceID = strings.TrimSpace(traceID)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !githubRecoveryTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl github %s: --trace-id must be 32 lowercase hex characters\n", action)
		return "", "", "", false
	}
	return targetID, reason, traceID, true
}

func githubRecoveryRequest(method, path string, input, output any, idempotent bool, headers http.Header) error {
	sess, err := loadOperatorSession()
	if err != nil {
		return err
	}
	return newOperatorHTTPClient(&sess).doJSONWithHeaders(
		context.Background(), method, path, input, output, idempotent, nil, headers,
	)
}

func validGithubCLIStatus(status string) bool {
	switch status {
	case "", "pending", "processing", "succeeded", "dead":
		return true
	default:
		return false
	}
}

func printGithubRecoveryStatus(response api.GithubRecoveryStatusResponse, status string) {
	_, _ = fmt.Fprintf(osStdout, "deliveries=%d check_updates=%d status=%s\n", len(response.Deliveries), len(response.CheckUpdates), status)
	for _, item := range response.Deliveries {
		_, _ = fmt.Fprintf(osStdout, "delivery %s event=%s status=%s attempts=%d updated=%s error=%q\n",
			item.DeliveryID, item.EventType, item.Status, item.Attempts, item.UpdatedAt.UTC().Format(time.RFC3339), item.LastError)
	}
	for _, item := range response.CheckUpdates {
		_, _ = fmt.Fprintf(osStdout, "check deployment=%s generation=%d status=%s attempts=%d updated=%s error=%q\n",
			item.DeploymentID, item.Generation, item.Status, item.Attempts, item.UpdatedAt.UTC().Format(time.RFC3339), item.LastError)
	}
}
