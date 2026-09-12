// Operator deployment incident controls. All reads and mutations go through
// apid; this command never opens PostgreSQL or accepts customer credentials.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

const dispatchDeployments = "deployments"

type operatorDeploymentMutationOutput struct {
	api.OperatorDeploymentMutationResponse
	TraceID string `json:"trace_id"`
}

func cmdDeploymentsDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl deployments: missing subcommand; want active|inspect|cancel|retry")
		return 2
	}
	switch args[0] {
	case "active":
		return cmdDeploymentsActive(args[1:])
	case "inspect":
		return cmdDeploymentsInspect(args[1:])
	case "cancel":
		return cmdDeploymentsCancel(args[1:])
	case "retry":
		return cmdDeploymentsRetry(args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdDeploymentsActive(args []string) int {
	fs := flag.NewFlagSet("active", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "optional account id (uuid)")
	appID := fs.String("app-id", "", "optional app id (uuid)")
	status := fs.String("status", "", "comma-separated deployment statuses (default incident states; use all for every state)")
	limit := fs.Int("limit", 50, "maximum deployments to return (1..200)")
	offset := fs.Int("offset", 0, "deployment pagination offset")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl deployments active: positional arguments are not accepted")
		return 2
	}
	for name, raw := range map[string]string{"account-id": *accountID, "app-id": *appID} {
		if raw != "" {
			if _, err := uuid.Parse(strings.TrimSpace(raw)); err != nil {
				_, _ = fmt.Fprintf(osStderr, "gregalectl deployments active: --%s must be a UUID when supplied\n", name)
				return 2
			}
		}
	}
	if *limit < 1 || *limit > 200 || *offset < 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl deployments active: --limit must be 1..200 and --offset must be non-negative")
		return 2
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}, "offset": {strconv.Itoa(*offset)}}
	if strings.TrimSpace(*accountID) != "" {
		query.Set("account_id", strings.TrimSpace(*accountID))
	}
	if strings.TrimSpace(*appID) != "" {
		query.Set("app_id", strings.TrimSpace(*appID))
	}
	if strings.TrimSpace(*status) != "" {
		query.Set("status", strings.TrimSpace(*status))
	}
	var response api.OperatorDeploymentListResponse
	if code := operatorDeploymentRequest("active", http.MethodGet, "/v1/admin/ops/deployments?"+query.Encode(), nil, &response, false, nil); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "deployments=%d account_id=%s app_id=%s limit=%d offset=%d next_offset=%d\n",
		len(response.Deployments), response.AccountID, response.AppID, response.Limit, response.Offset, response.NextOffset)
	for _, deployment := range response.Deployments {
		printOperatorDeployment(deployment)
	}
	return 0
}

func cmdDeploymentsInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	deploymentID := fs.String("deployment-id", "", "deployment id (uuid)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateOperatorDeploymentID(fs, "inspect", *deploymentID)
	if !ok {
		return 2
	}
	var response api.OperatorDeploymentDetailResponse
	if code := operatorDeploymentRequest("inspect", http.MethodGet, "/v1/admin/ops/deployments/"+url.PathEscape(id), nil, &response, false, nil); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printOperatorDeployment(response.Deployment)
	if response.Build != nil {
		_, _ = fmt.Fprintf(osStdout, "build id=%s status=%s failure_class=%s cache_status=%s enqueued_at=%s started_at=%s finished_at=%s\n",
			response.Build.ID, response.Build.Status, response.Build.FailureClass, response.Build.CacheStatus,
			response.Build.EnqueuedAt, response.Build.StartedAt, response.Build.FinishedAt)
	}
	if response.Stage != nil {
		_, _ = fmt.Fprintf(osStdout, "stage current=%s current_started_at=%s retry_requested_stage=%s history=%d\n",
			response.Stage.Current, response.Stage.CurrentStartedAt, response.Stage.RetryRequestedStage, len(response.Stage.History))
		for _, item := range response.Stage.History {
			_, _ = fmt.Fprintf(osStdout, "stage_item name=%s status=%s duration_ms=%d started_at=%s ended_at=%s reason=%s\n",
				item.Name, item.Status, item.DurationMS, item.StartedAt, item.EndedAt, item.Reason)
		}
	}
	return 0
}

func cmdDeploymentsCancel(args []string) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	deploymentID := fs.String("deployment-id", "", "deployment id (uuid)")
	reason := fs.String("reason", "", "durable audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	yes := fs.Bool("yes", false, "acknowledge cancelling customer deployment work")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateOperatorDeploymentID(fs, "cancel", *deploymentID)
	if !ok {
		return 2
	}
	cleanReason, traceID, ok := validateDeploymentMutationFlags("cancel", *reason, *traceIDFlag, *yes)
	if !ok {
		return 2
	}
	query := url.Values{"confirm": {"true"}, "reason": {cleanReason}}
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.OperatorDeploymentMutationResponse
	path := "/v1/admin/ops/deployments/" + url.PathEscape(id) + "/cancel?" + query.Encode()
	if code := operatorDeploymentRequest("cancel", http.MethodPost, path, nil, &response, true, headers); code != 0 {
		return code
	}
	output := operatorDeploymentMutationOutput{OperatorDeploymentMutationResponse: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	_, _ = fmt.Fprintf(osStdout, "cancelled deployment_id=%s app=%s account_id=%s status=%s reason=%s trace_id=%s\n",
		response.Deployment.ID, response.Deployment.AppSlug, response.Deployment.AccountID, response.Deployment.Status, response.Reason, traceID)
	return 0
}

func cmdDeploymentsRetry(args []string) int {
	fs := flag.NewFlagSet("retry", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	deploymentID := fs.String("deployment-id", "", "failed deployment id (uuid)")
	fromStage := fs.String("from-stage", "source_download", "stage to restart from")
	reason := fs.String("reason", "", "durable audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	yes := fs.Bool("yes", false, "acknowledge enqueueing customer deployment work")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateOperatorDeploymentID(fs, "retry", *deploymentID)
	if !ok {
		return 2
	}
	cleanReason, traceID, ok := validateDeploymentMutationFlags("retry", *reason, *traceIDFlag, *yes)
	if !ok {
		return 2
	}
	if strings.TrimSpace(*fromStage) == "" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl deployments retry: --from-stage is required")
		return 2
	}
	query := url.Values{"confirm": {"true"}, "from_stage": {strings.TrimSpace(*fromStage)}, "reason": {cleanReason}}
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.OperatorDeploymentMutationResponse
	path := "/v1/admin/ops/deployments/" + url.PathEscape(id) + "/retry?" + query.Encode()
	if code := operatorDeploymentRequest("retry", http.MethodPost, path, nil, &response, true, headers); code != 0 {
		return code
	}
	output := operatorDeploymentMutationOutput{OperatorDeploymentMutationResponse: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	_, _ = fmt.Fprintf(osStdout, "retry_enqueued deployment_id=%s app=%s account_id=%s status=%s from_stage=%s reason=%s trace_id=%s\n",
		response.Deployment.ID, response.Deployment.AppSlug, response.Deployment.AccountID, response.Deployment.Status,
		strings.TrimSpace(*fromStage), response.Reason, traceID)
	return 0
}

func validateOperatorDeploymentID(fs *flag.FlagSet, action, raw string) (string, bool) {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: positional arguments are not accepted\n", action)
		return "", false
	}
	id := strings.TrimSpace(raw)
	if _, err := uuid.Parse(id); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: --deployment-id is required and must be a UUID\n", action)
		return "", false
	}
	return id, true
}

func validateDeploymentMutationFlags(action, rawReason, rawTrace string, acknowledged bool) (string, string, bool) {
	reason := strings.TrimSpace(rawReason)
	if !operatorJobReasonShape.MatchString(reason) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: --reason is required and must match [a-z0-9_]{1,64}\n", action)
		return "", "", false
	}
	if !acknowledged {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: --yes required\n", action)
		return "", "", false
	}
	traceID := strings.TrimSpace(rawTrace)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !operatorJobTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: --trace-id must be 32 lowercase hex characters\n", action)
		return "", "", false
	}
	return reason, traceID, true
}

func operatorDeploymentRequest(action, method, path string, input, output any, idempotent bool, headers http.Header) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: %v\n", action, err)
		return 1
	}
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), method, path, input, output, idempotent, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl deployments %s: %v\n", action, err)
		return 1
	}
	return 0
}

func printOperatorDeployment(deployment api.OperatorDeployment) {
	_, _ = fmt.Fprintf(osStdout, "deployment_id=%s app=%s app_id=%s account_id=%s status=%s kind=%s created_at=%s priority=%d",
		deployment.ID, deployment.AppSlug, deployment.AppID, deployment.AccountID, deployment.Status, deployment.Kind,
		deployment.CreatedAt, deployment.Priority)
	if deployment.ErrorCode != "" {
		_, _ = fmt.Fprintf(osStdout, " error_code=%s", deployment.ErrorCode)
	}
	if deployment.Error != "" {
		_, _ = fmt.Fprintf(osStdout, " error=%q", deployment.Error)
	}
	_, _ = fmt.Fprintln(osStdout)
}
