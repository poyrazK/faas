package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// cmdAccountsComplimentary gives an account a paid plan without a
// subscription, or with --plan none returns it to Free (ADR-20261111140000).
// A complimentary account is not billed and stops at the plan's included
// amounts; --until returns it to Free at that time.
func cmdAccountsComplimentary(args []string) int {
	const action = "complimentary"
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account id (uuid)")
	plan := fs.String("plan", "", "hobby, pro or scale; none returns the account to Free")
	until := fs.String("until", "", "RFC 3339 time the account returns to Free (optional)")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge the plan change")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateAccountCommand(fs, action, *accountID)
	if !ok {
		return 2
	}
	cleanPlan := strings.TrimSpace(*plan)
	if cleanPlan != "none" && !api.Plan(cleanPlan).IsPaid() {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --plan must be hobby, pro, scale or none\n", action)
		return 2
	}
	cleanUntil := strings.TrimSpace(*until)
	if cleanUntil != "" {
		if _, err := time.Parse(time.RFC3339, cleanUntil); err != nil || cleanPlan == "none" {
			_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --until must be an RFC 3339 time, and only with a paid --plan\n", action)
			return 2
		}
	}
	cleanReason := strings.TrimSpace(*reason)
	if !accountReasonShape.MatchString(cleanReason) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --reason is required and must match [a-z0-9_]{1,64}\n", action)
		return 2
	}
	traceID := strings.TrimSpace(*traceIDFlag)
	if traceID == "" {
		traceID = wire.NewTraceID()
	}
	if !*ack || !accountTraceIDShape.MatchString(traceID) {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: --yes is required, and --trace-id must be 32 lowercase hex characters\n", action)
		return 2
	}
	return postAccountComplimentary(action, id, cleanPlan, cleanUntil, cleanReason, traceID)
}

func postAccountComplimentary(action, id, plan, until, reason, traceID string) int {
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	query := url.Values{"confirm": {"true"}, "plan": {plan}, "reason": {reason}}
	if until != "" {
		query.Set("until", until)
	}
	path := "/v1/admin/ops/accounts/" + url.PathEscape(id) + "/" + action + "?" + query.Encode()
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.ObsComplimentaryResponse
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), http.MethodPost, path, nil, &response, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	expires := "never"
	if response.ExpiresAt != nil {
		expires = response.ExpiresAt.UTC().Format(time.RFC3339)
	}
	_, _ = fmt.Fprintf(osStdout, "action=%s account_id=%s plan=%s complimentary=%t expires_at=%s trace_id=%s\n",
		action, response.AccountID, response.Plan, response.Complimentary, expires, traceID)
	return 0
}
