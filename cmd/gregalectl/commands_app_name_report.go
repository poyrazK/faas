package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdAccountsAppNameReport is `gregalectl accounts app-name-report`: the
// existing apps whose names the app name blocklist matches. It changes
// nothing; --blocklist previews a list before `gregalectl config set
// app_name_blocklist`.
func cmdAccountsAppNameReport(args []string) int {
	const label = "accounts app-name-report"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	blocklist := fs.String("blocklist", "", "report against these entries instead of the live app_name_blocklist")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	path := "/v1/admin/app-names/report"
	if entries := strings.TrimSpace(*blocklist); entries != "" {
		if _, err := api.ParseAppNameBlocklist(entries); err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --blocklist: %v\n", label, err)
			return 2
		}
		path += "?" + url.Values{"blocklist": {entries}}.Encode()
	}
	var r api.AppNameReportResponse
	if code := operatorAPIRequest(label, http.MethodGet, path, nil, &r, "", false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(r)
	}
	printAppNameReport(r)
	return 0
}

func printAppNameReport(r api.AppNameReportResponse) {
	out := func(format string, a ...any) { _, _ = fmt.Fprintf(osStdout, format, a...) }
	out("source=%s entries=%d matches=%d", r.Source, r.Entries, len(r.Matches))
	if r.Truncated {
		out(" truncated=true")
	}
	out("\n")
	for _, m := range r.Matches {
		out("app=%s app_id=%s account_id=%s status=%s entry=%s%s\n", m.Slug, m.AppID, m.AccountID, m.Status, m.Entry, orgSuffix(m.OrgID))
	}
}
