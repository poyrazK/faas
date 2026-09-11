// commands_accounts.go — authenticated operator account support tools.
//
// Read commands use the bounded, PII-redacted observability projections.
// Mutations use the strict admin API path so MFA step-up, idempotency, audit,
// and trace correlation stay in apid instead of becoming direct DB writes.
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

const dispatchAccounts = "accounts"

var (
	accountReasonShape  = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	accountTraceIDShape = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

type accountMutationOutput struct {
	api.ObsAccountMutationResponse
	TraceID string `json:"trace_id"`
}

func cmdAccountsDispatch(args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl accounts: missing subcommand; want list|show|360|activity|suspend|restore|revoke-sessions")
		return 2
	}
	switch args[0] {
	case "list":
		return cmdAccountsList(args[1:])
	case "show":
		return cmdAccountsShow(args[1:])
	case "360":
		return cmdAccounts360(args[1:])
	case "activity":
		return cmdAccountsActivity(args[1:])
	case "suspend", "restore", "revoke-sessions":
		return cmdAccountsMutation(args[0], args[1:])
	default:
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts: unknown subcommand %q\n", args[0])
		return 2
	}
}

func cmdAccountsList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	limit := fs.Int("limit", 100, "maximum accounts to return (1..500)")
	cursor := fs.String("cursor", "", "pagination cursor from the previous response")
	plan := fs.String("plan", "", "filter by plan")
	status := fs.String("status", "", "filter by account status")
	includePII := fs.Bool("include-pii", false, "include account email (audited by apid)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl accounts list: positional arguments are not accepted")
		return 2
	}
	if *limit < 1 || *limit > 500 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl accounts list: --limit must be between 1 and 500")
		return 2
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if value := strings.TrimSpace(*cursor); value != "" {
		query.Set("cursor", value)
	}
	if value := strings.TrimSpace(*plan); value != "" {
		query.Set("plan", value)
	}
	if value := strings.TrimSpace(*status); value != "" {
		query.Set("status", value)
	}
	if *includePII {
		query.Set("include_pii", "1")
	}
	var response api.ObsTenantListResponse
	if code := accountGet("list", "/v1/admin/obs/tenants?"+query.Encode(), &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	for _, account := range response.Items {
		printAccountRow(account)
	}
	if response.NextCursor != "" {
		_, _ = fmt.Fprintf(osStdout, "next_cursor=%s\n", response.NextCursor)
	}
	return 0
}

func cmdAccountsShow(args []string) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account id (uuid)")
	includePII := fs.Bool("include-pii", false, "include account email (audited by apid)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateAccountCommand(fs, "show", *accountID)
	if !ok {
		return 2
	}
	path := "/v1/admin/obs/tenants/" + url.PathEscape(id)
	if *includePII {
		path += "?include_pii=1"
	}
	var response api.ObsTenantDetailResponse
	if code := accountGet("show", path, &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printAccountRow(response.Account)
	_, _ = fmt.Fprintf(osStdout, "apps=%d orgs=%d api_keys_active=%d api_keys_revoked=%d sessions_active=%d sessions_revoked=%d\n",
		len(response.Apps), len(response.Orgs), response.APIKeys.Active, response.APIKeys.Revoked, response.Sessions.Active, response.Sessions.Revoked)
	for _, app := range response.Apps {
		_, _ = fmt.Fprintf(osStdout, "app id=%s slug=%s status=%s deployments=%d\n", app.ID, app.Slug, app.Status, app.Deployments)
	}
	for _, org := range response.Orgs {
		_, _ = fmt.Fprintf(osStdout, "org id=%s slug=%s role=%s\n", org.ID, org.Slug, org.Role)
	}
	return 0
}

func cmdAccounts360(args []string) int {
	fs := flag.NewFlagSet("360", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account id (uuid)")
	month := fs.String("month", "", "usage month (YYYY-MM; current month when omitted)")
	includePII := fs.Bool("include-pii", false, "include account email (audited by apid)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateAccountCommand(fs, "360", *accountID)
	if !ok {
		return 2
	}
	query := url.Values{}
	if value := strings.TrimSpace(*month); value != "" {
		if _, err := time.Parse("2006-01", value); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl accounts 360: --month must use YYYY-MM")
			return 2
		}
		query.Set("month", value)
	}
	if *includePII {
		query.Set("include_pii", "1")
	}
	path := "/v1/admin/obs/tenants/" + url.PathEscape(id) + "/360"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var response api.ObsTenant360Response
	if code := accountGet("360", path, &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	printAccountRow(response.Account)
	_, _ = fmt.Fprintf(osStdout, "month=%s gb_hours=%.3f included_gb_hours=%d overage_gb_hours=%.3f overage_cents=%d cpu_hours=%.3f requests=%d cold_boots=%d\n",
		response.Usage.Month, response.Usage.UsedGBHours, response.Usage.IncludedGBHours, response.Usage.OverageGBHours,
		response.Usage.OverageCents, response.Usage.UsedCPUHours, response.Usage.Requests, response.Usage.ColdBootTotal)
	_, _ = fmt.Fprintf(osStdout, "billing current_overage_cents=%d active_credits_cents=%d invoices=%d\n",
		response.Billing.CurrentMonthOverageCents, response.Billing.ActiveCreditsCents, len(response.Billing.Invoices))
	return 0
}

func cmdAccountsActivity(args []string) int {
	fs := flag.NewFlagSet("activity", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account id (uuid)")
	limit := fs.Int("limit", 50, "maximum invocations and audit events (1..200)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateAccountCommand(fs, "activity", *accountID)
	if !ok {
		return 2
	}
	if *limit < 1 || *limit > 200 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl accounts activity: --limit must be between 1 and 200")
		return 2
	}
	path := "/v1/admin/obs/tenants/" + url.PathEscape(id) + "/activity?limit=" + strconv.Itoa(*limit)
	var response api.ObsTenantActivityResponse
	if code := accountGet("activity", path, &response); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	_, _ = fmt.Fprintf(osStdout, "account_id=%s generated_at=%s invocations=%d audit_events=%d\n",
		response.AccountID, response.GeneratedAt.Format(time.RFC3339), len(response.Invocations), len(response.AuditEvents))
	for _, invocation := range response.Invocations {
		_, _ = fmt.Fprintf(osStdout, "invocation id=%s app=%s state=%s source=%s method=%s path=%s outcome=%s attempts=%d created_at=%s\n",
			invocation.ID, invocation.AppSlug, invocation.State, invocation.Source, invocation.Method, invocation.Path,
			invocation.Outcome, invocation.Attempts, invocation.CreatedAt.Format(time.RFC3339))
	}
	for _, event := range response.AuditEvents {
		_, _ = fmt.Fprintf(osStdout, "audit id=%s at=%s kind=%s actor=%s subject=%s\n",
			event.ID, event.At.Format(time.RFC3339), event.Kind, event.Actor, event.Subject)
	}
	return 0
}

func cmdAccountsMutation(action string, args []string) int {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account id (uuid)")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge the account mutation")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateAccountCommand(fs, action, *accountID)
	if !ok {
		return 2
	}
	cleanReason := strings.TrimSpace(*reason)
	if !accountReasonShape.MatchString(cleanReason) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --reason is required and must match [a-z0-9_]{1,64}\n", action)
		return 2
	}
	if !*ack {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --yes required\n", action)
		return 2
	}
	traceID := strings.TrimSpace(*traceIDFlag)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !accountTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --trace-id must be 32 lowercase hex characters\n", action)
		return 2
	}
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	path := "/v1/admin/ops/accounts/" + url.PathEscape(id) + "/" + action + "?confirm=true&reason=" + url.QueryEscape(cleanReason)
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.ObsAccountMutationResponse
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), http.MethodPost, path, nil, &response, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	output := accountMutationOutput{ObsAccountMutationResponse: response, TraceID: traceID}
	if jsonEnabled() {
		return emitOperatorJSON(output)
	}
	_, _ = fmt.Fprintf(osStdout, "action=%s account_id=%s status=%s revoked_sessions=%d trace_id=%s\n",
		response.Action, response.Account.AccountID, response.Account.Status, response.RevokedSessions, traceID)
	return 0
}

func validateAccountCommand(fs *flag.FlagSet, action, rawID string) (string, bool) {
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: positional arguments are not accepted\n", action)
		return "", false
	}
	id := strings.TrimSpace(rawID)
	if id == "" {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --account-id required\n", action)
		return "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --account-id must be a UUID\n", action)
		return "", false
	}
	return id, true
}

func accountGet(action, path string, output any) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	if err := newOperatorHTTPClient(&sess).doJSON(context.Background(), http.MethodGet, path, nil, output, false, nil); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	return 0
}

func printAccountRow(account api.ObsTenantRow) {
	_, _ = fmt.Fprintf(osStdout, "account_id=%s status=%s plan=%s apps=%d live_deployments=%d mfa=%t",
		account.AccountID, account.Status, account.Plan, account.AppsCount, account.DeploymentsLiveCount, account.MFAEnrolled)
	if account.OrgSlug != "" {
		_, _ = fmt.Fprintf(osStdout, " org=%s", account.OrgSlug)
	}
	if account.Email != "" {
		_, _ = fmt.Fprintf(osStdout, " email=%s", account.Email)
	}
	_, _ = fmt.Fprintln(osStdout)
}
