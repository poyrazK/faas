// adr: 20261019102108 — Data API usage reporting, owner monthly caps and the usage view.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	dataAPIUsageURLKey    = "DATA_API_USAGE_URL"
	dataAPIMonthlyCapsKey = "DATA_API_MONTHLY_CAPS"
)

// dataAPIUsageURL is the platform origin a runtime reports usage to. The
// runtime accepts only an HTTPS origin, so a development API over HTTP gets
// no reporting rather than a runtime that refuses to start.
func dataAPIUsageURL(base string) (string, bool) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", false
	}
	return "https://" + u.Host + "/", true
}

// dataAPIMonthlyCaps is the version-1 runtime contract for owner caps.
type dataAPIMonthlyCaps struct {
	Version           int    `json:"version"`
	Requests          *int64 `json:"requests,omitempty"`
	AnonymousRequests *int64 `json:"anonymous_requests,omitempty"`
}

// parseDataAPIMonthlyCaps reads "requests=N", "anonymous=N" or both, comma-separated.
func parseDataAPIMonthlyCaps(value string) (*dataAPIMonthlyCaps, error) {
	caps := &dataAPIMonthlyCaps{Version: 1}
	for _, part := range strings.Split(value, ",") {
		name, raw, ok := strings.Cut(part, "=")
		n, err := strconv.ParseInt(raw, 10, 64)
		if !ok || err != nil || n < 1 || n > api.DataAPIUsageMonthlyCapMax {
			return nil, fmt.Errorf("%q must be requests=N or anonymous=N with N from 1 to %d", part, int64(api.DataAPIUsageMonthlyCapMax))
		}
		switch {
		case name == "requests" && caps.Requests == nil:
			caps.Requests = &n
		case name == "anonymous" && caps.AnonymousRequests == nil:
			caps.AnonymousRequests = &n
		default:
			return nil, fmt.Errorf("%q names an unknown or repeated cap", part)
		}
	}
	return caps, nil
}

// configureDataAPIUsage applies --usage-reporting and --monthly-caps. Caps
// need reporting, so the URL is stored before caps and removed after them.
func configureDataAPIUsage(ctx context.Context, client *api.Client, app, scope, reporting, caps string, receipt *dataAPIConfigureReceipt) error {
	var parsed *dataAPIMonthlyCaps
	if caps != "" && caps != "off" {
		var err error
		if parsed, err = parseDataAPIMonthlyCaps(caps); err != nil {
			return err
		}
	}
	if reporting == "on" || parsed != nil {
		origin, ok := dataAPIUsageURL(client.BaseURL())
		if !ok {
			return errors.New("usage reporting needs an HTTPS platform API; this CLI targets " + client.BaseURL())
		}
		if err := client.SetSecretWithScope(ctx, app, dataAPIUsageURLKey, origin, scope); err != nil {
			return err
		}
	}
	if parsed != nil {
		encoded, err := json.Marshal(parsed)
		if err != nil {
			return err
		}
		if err = client.SetSecretWithScope(ctx, app, dataAPIMonthlyCapsKey, string(encoded), scope); err != nil {
			return err
		}
		receipt.MonthlyCaps = parsed
	}
	if caps == "off" || reporting == "off" {
		if err := unsetDataAPISetting(ctx, client, app, dataAPIMonthlyCapsKey, scope); err != nil {
			return err
		}
	}
	if reporting == "off" {
		if err := unsetDataAPISetting(ctx, client, app, dataAPIUsageURLKey, scope); err != nil {
			return err
		}
	}
	if reporting != "" {
		receipt.UsageReporting = reporting
		receipt.Changed = append(receipt.Changed, "usage_reporting")
	}
	if caps != "" {
		receipt.Changed = append(receipt.Changed, "monthly_caps")
	}
	return nil
}

func cmdDataAPIUsage(args []string) int {
	fs := newFlagSet("data-api usage", flag.ContinueOnError)
	since := fs.String("since", "7d", "lookback such as 24h, 7d or 30d (at most 90d)")
	granularity := fs.String("granularity", "", "hour or day (default: hour up to 48h, else day)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || !dataAPIDiagnosticAllowed(*granularity, "", "hour", "day") {
		PrintUsage(os.Stderr, "usage: gregale data-api usage NAME [--since 7d] [--granularity hour|day]", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	usage, err := client.GetAppDataAPIUsage(context.Background(), fs.Arg(0), *since, *granularity)
	if err != nil {
		return printErr("Could not read Data API usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(usage))
	}
	if err := writeDataAPIUsage(usage, *since); err != nil {
		return printErr("Could not write usage", err)
	}
	return 0
}

func writeDataAPIUsage(u api.DataAPIUsage, since string) error {
	t := u.Totals
	requests := t.UserRequests + t.AnonymousRequests + t.ServerRequests
	unit := "UTC hours"
	if u.Granularity == "day" {
		unit = "UTC days"
	}
	lines := []string{
		fmt.Sprintf("Data API usage for %s, last %s (%s)", u.App, since, unit),
		fmt.Sprintf("Requests: %d (user %d, anonymous %d, server %d); rejected %d, errors %d, refused by caps %d", requests, t.UserRequests, t.AnonymousRequests, t.ServerRequests, t.RejectedRequests, t.ErrorRequests, t.CappedRequests),
		fmt.Sprintf("Rows returned: %d; response bytes: %d", t.RowsReturned, t.ResponseBytes),
		fmt.Sprintf("This month (%s): %d requests, %d anonymous", u.MonthToDate.Month, u.MonthToDate.Requests, u.MonthToDate.AnonymousRequests),
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(osStdout, line); err != nil {
			return err
		}
	}
	if requests+t.RejectedRequests+t.ErrorRequests+t.CappedRequests == 0 {
		_, err := fmt.Fprintf(osStdout, "No usage reported in this window. Runtimes report usage when reporting is on: gregale data-api configure %s --usage-reporting on\n", u.App)
		return err
	}
	w := tabwriter.NewWriter(osStdout, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "START\tUSER\tANONYMOUS\tSERVER\tREJECTED\tERRORS\tCAPPED\tROWS\tBYTES")
	layout := "2006-01-02 15:00"
	if u.Granularity == "day" {
		layout = "2006-01-02"
	}
	for _, b := range u.Buckets {
		_, _ = fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", b.Start.UTC().Format(layout), b.UserRequests, b.AnonymousRequests, b.ServerRequests, b.RejectedRequests, b.ErrorRequests, b.CappedRequests, b.RowsReturned, b.ResponseBytes)
	}
	return w.Flush()
}
