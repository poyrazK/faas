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

func cmdDataRepairSources(args []string) int {
	fs := newFlagSet("data-repair sources", flag.ContinueOnError)
	app := fs.String("app", "", "hosted application slug; uses the normal login")
	base := fs.String("url", "http://127.0.0.1:8796", "executor URL")
	tokenEnv := fs.String("token-env", "GREGALE_REPAIR_TOKEN", "executor token environment variable")
	fs.String("environment", "", "configured hosted environment")
	fs.String("customer", "", "configured hosted customer UUID; empty selects account scope")
	scopes := fs.Bool("scopes", false, "list configured hosted source scopes")
	q := datarepair.SourceQuery{}
	fs.StringVar(&q.Release, "release", "", "exact captured release")
	fs.StringVar(&q.Table, "table", "", "exact enrolled qualified table name")
	fs.StringVar(&q.Cursor, "cursor", "", "next_cursor from the preceding page; retain its filters and limit")
	fs.IntVar(&q.Limit, "limit", api.DataRepairSourcePageSize, "maximum sources per page")
	since := fs.String("since", "", "inclusive RFC3339 capture time")
	before := fs.String("before", "", "exclusive RFC3339 capture time")
	flags, pos := splitArgsForFlags(args, "scopes")
	if fs.Parse(flags) != nil || len(pos) != 0 || fs.NArg() != 0 || q.Limit < 1 {
		return 1
	}
	for _, bound := range []struct {
		raw    string
		target *time.Time
	}{{*since, &q.Since}, {*before, &q.Before}} {
		if bound.raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, bound.raw)
		if err != nil || parsed.IsZero() {
			return printErr("Use RFC3339 capture times", datarepair.ErrInvalid)
		}
		*bound.target = parsed
	}
	q, err := q.Normalize()
	if err != nil {
		return printErr("Invalid repair source filters or page size", err)
	}
	values := repairapi.SourceQueryValues(q)
	token := os.Getenv(*tokenEnv)
	endpoint := *base
	path := "/v1/data-repair/sources"
	invalid := false
	fs.Visit(func(f *flag.Flag) {
		if *app == "" && (f.Name == "environment" || f.Name == "customer" || f.Name == "scopes") {
			invalid = true
		}
		if *app != "" && (f.Name == "url" || f.Name == "token-env") {
			invalid = true
		}
		if f.Name == "environment" || f.Name == "customer" {
			values.Set(f.Name, f.Value.String())
		}
	})
	if invalid {
		return printErr("Use hosted scope flags with --app, or executor --url/--token-env", datarepair.ErrInvalid)
	}
	if *app != "" {
		endpoint, token = apiBase(), loadToken()
		path = "/v1/apps/" + url.PathEscape(*app) + "/data-repair/sources"
		if *scopes {
			path = "/v1/apps/" + url.PathEscape(*app) + "/data-repair/source-scopes"
			values = url.Values{}
		}
	}
	u, err := repairEndpoint(endpoint)
	if err != nil || token == "" {
		return printErr("Use an authorized HTTPS API or loopback executor", datarepair.ErrInvalid)
	}
	// Keep escaped slugs intact; never use a returned URL as a credential target.
	endpoint = strings.TrimRight(u.String(), "/") + path
	if len(values) != 0 {
		endpoint += "?" + values.Encode()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	raw, err := managedRepairRequest(ctx, endpoint, token, nil, "")
	if err != nil {
		return printErr("Cannot discover repair sources", err)
	}
	if len(raw) > api.DataRepairSourceMaxResponseBytes {
		return printErr("Repair source response exceeded its bound", datarepair.ErrUnavailable)
	}
	var result any
	if *scopes {
		var response repairapi.SourceScopesResponse
		if json.Unmarshal(raw, &response) != nil || response.Scopes == nil {
			return printErr("Invalid repair source scopes", datarepair.ErrUnavailable)
		}
		result = response
	} else {
		var page datarepair.SourcePage
		if json.Unmarshal(raw, &page) != nil || page.Scope.Validate() != nil || page.Items == nil || len(page.Items) > q.Limit {
			return printErr("Invalid repair source response", datarepair.ErrUnavailable)
		}
		result = page
	}
	pretty, _ := json.MarshalIndent(result, "", "  ")
	if _, err := fmt.Fprintln(osStdout, string(pretty)); err != nil {
		return printErr("Cannot write repair sources", err)
	}
	return 0
}
