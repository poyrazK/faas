package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	repairapi "github.com/onebox-faas/faas/pkg/apid/datarepair"
	"github.com/onebox-faas/faas/pkg/datarepair"
)

func cmdDataRepairHistory(args []string) int {
	fs := newFlagSet("data-repair history", flag.ContinueOnError)
	app := fs.String("app", "", "hosted application slug; uses the normal login")
	base := fs.String("url", "http://127.0.0.1:8796", "executor URL")
	tokenEnv := fs.String("token-env", "GREGALE_REPAIR_TOKEN", "executor token environment variable")
	fs.String("environment", "", "configured hosted environment")
	fs.String("customer", "", "configured hosted customer UUID; empty selects account scope")
	q := datarepair.HistoryQuery{}
	fs.IntVar(&q.Limit, "limit", api.DataRepairSourcePageSize, "maximum plans per page")
	fs.StringVar(&q.Cursor, "cursor", "", "next_cursor; retain the source, scope and limit")
	flags, pos := splitArgsForFlags(args)
	if fs.Parse(flags) != nil || len(pos) != 1 || fs.NArg() != 0 || !dataRepairUUID(pos[0]) || q.Limit < 1 {
		return 1
	}
	q, err := q.Normalize()
	if err != nil {
		return printErr("Invalid repair history pagination", err)
	}
	values := repairapi.HistoryQueryValues(q)
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if *app == "" && (f.Name == "environment" || f.Name == "customer") || *app != "" && (f.Name == "url" || f.Name == "token-env") {
			invalid = true
		}
		if f.Name == "environment" || f.Name == "customer" {
			values.Set(f.Name, f.Value.String())
		}
	})
	if invalid {
		return printErr("Use hosted scope flags with --app, or executor --url/--token-env", datarepair.ErrInvalid)
	}
	endpoint, token := *base, os.Getenv(*tokenEnv)
	path := "/v1/data-repair/sources/" + pos[0] + "/history"
	if *app != "" {
		endpoint, token = apiBase(), loadToken()
		path = "/v1/apps/" + url.PathEscape(*app) + "/data-repair/sources/" + pos[0] + "/history"
	}
	u, err := repairEndpoint(endpoint)
	if err != nil || token == "" {
		return printErr("Use an authorized HTTPS API or loopback executor", datarepair.ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	raw, err := managedRepairRequest(ctx, strings.TrimRight(u.String(), "/")+path+"?"+values.Encode(), token, nil, "")
	if err != nil {
		return printErr("Cannot discover repair history", err)
	}
	var p datarepair.HistoryPage
	if len(raw) > api.DataRepairSourceMaxResponseBytes || json.Unmarshal(raw, &p) != nil || !p.Valid(q.Limit) || p.SourceOperationID != pos[0] || !repairHistoryScopeMatches(p.Scope, values) {
		return printErr("Invalid repair history response", datarepair.ErrUnavailable)
	}
	pretty, _ := json.MarshalIndent(p, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(pretty)); err != nil {
		return printErr("Cannot write repair history", err)
	}
	return 0
}

func repairHistoryScopeMatches(s datarepair.Scope, values url.Values) bool {
	return s.Validate() == nil && (!values.Has("environment") || s.Environment == values.Get("environment")) &&
		(!values.Has("customer") || s.PlatformTenantID == values.Get("customer"))
}

func cmdDataRepairHistorical(ctx context.Context, verb, base, token, source, id, digest string, scope url.Values) int {
	endpoint := base + "/sources/" + source + "/plans/" + id
	var body any
	if verb == "verify" {
		endpoint += "/verify"
		body = map[string]string{"digest": digest}
	}
	if len(scope) > 0 {
		endpoint += "?" + scope.Encode()
	}
	raw, err := managedRepairRequest(ctx, endpoint, token, body, "")
	if err != nil {
		return printErr("Cannot read retained repair history", err)
	}
	if verb == "verify" {
		var report datarepair.VerificationReport
		if json.Unmarshal(raw, &report) != nil || report.SourceOperationID != source || !repairHistoryScopeMatches(report.Scope, scope) {
			return printErr("Verification did not match the discovered source", datarepair.ErrUnavailable)
		}
		return printDataRepairVerification(raw, id, digest, false)
	}
	var response repairapi.HistoricalPlanResponse
	if json.Unmarshal(raw, &response) != nil || response.Plan.ID != id || response.Plan.SourceOperationID != source ||
		!repairHistoryScopeMatches(response.Plan.Scope, scope) || response.Execution == nil ||
		!response.Execution.Valid(id) || !dataRepairDigest(response.Plan.Digest) {
		return printErr("Invalid retained repair plan", datarepair.ErrUnavailable)
	}
	pretty, _ := json.MarshalIndent(response, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(pretty)); err != nil {
		return printErr("Cannot write retained repair plan", err)
	}
	return 0
}

func dataRepairDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}
	return true
}
