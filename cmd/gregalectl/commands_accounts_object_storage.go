package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// cmdAccountsObjectStorageCeiling raises a paid account's (or
// resource-owning org's) object-storage ceiling above its plan's, or returns
// it to the plan's with --plan (ADR-20261101214152). The spend cap still
// tightens the ceiling.
func cmdAccountsObjectStorageCeiling(args []string) int {
	const action = "object-storage-ceiling"
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	accountID := fs.String("account-id", "", "account or resource-owning org id (uuid)")
	gib := fs.Int64("gib", 0, "the ceiling in GiB, in place of the plan's")
	plan := fs.Bool("plan", false, "return to the plan's ceiling")
	reason := fs.String("reason", "", "audit reason slug ([a-z0-9_]{1,64})")
	traceIDFlag := fs.String("trace-id", "", "OTel 32-char-hex trace id (auto-generated when empty)")
	ack := fs.Bool("yes", false, "acknowledge the ceiling change")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	id, ok := validateAccountCommand(fs, action, *accountID)
	if !ok {
		return 2
	}
	if (*gib > 0) == *plan {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: pass exactly one of --gib N (N > 0) or --plan\n", action)
		return 2
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
	sess, err := loadOperatorSession()
	if err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	ceiling := "plan"
	if !*plan {
		ceiling = strconv.FormatInt(*gib, 10)
	}
	path := "/v1/admin/ops/accounts/" + url.PathEscape(id) + "/" + action + "?confirm=true&gib=" + ceiling + "&reason=" + url.QueryEscape(cleanReason)
	headers := make(http.Header)
	headers.Set(operatorTraceIDHeader, traceID)
	var response api.ObsObjectStorageCeilingResponse
	if err := newOperatorHTTPClient(&sess).doJSONWithHeaders(context.Background(), http.MethodPost, path, nil, &response, true, nil, headers); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl accounts %s: %v\n", action, err)
		return 1
	}
	if jsonEnabled() {
		return emitOperatorJSON(response)
	}
	grant := "none"
	if response.GrantBytes != nil {
		grant = strconv.FormatInt(*response.GrantBytes>>30, 10) + "GiB"
	}
	_, _ = fmt.Fprintf(osStdout, "action=%s owner_id=%s plan=%s plan_ceiling=%dGiB grant=%s ceiling=%dGiB trace_id=%s\n",
		action, response.OwnerID, response.Plan, response.PlanCeilingBytes>>30, grant, response.CeilingBytes>>30, traceID)
	return 0
}
