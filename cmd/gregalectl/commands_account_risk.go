package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdAccountsRisk is `gregalectl accounts risk` (ADR-20261115090000): an
// account's hold, sign-up record, links to and from abuse-held accounts,
// strikes in the hold window and abuse events of the last 30 days.
func cmdAccountsRisk(args []string) int {
	const label = "accounts risk"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	accountID := fs.String("account-id", "", "account id (uuid)")
	includePII := fs.Bool("include-pii", false, "include email, sign-up address and user agent (audited by apid)")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	id, ok := validOperatorUUID(label, "account-id", strings.TrimSpace(*accountID))
	if !ok {
		return 2
	}
	path := "/v1/admin/accounts/" + url.PathEscape(id) + "/risk"
	if *includePII {
		path += "?include_pii=1"
	}
	var r api.AccountRiskResponse
	if code := operatorAPIRequest(label, http.MethodGet, path, nil, &r, "", false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(r)
	}
	printAccountRisk(r)
	return 0
}

func printAccountRisk(r api.AccountRiskResponse) {
	out := func(format string, a ...any) { _, _ = fmt.Fprintf(osStdout, format, a...) }
	out("account_id=%s status=%s plan=%s created_at=%s", r.AccountID, r.Status, r.Plan, r.CreatedAt.UTC().Format(time.RFC3339))
	if r.Email != "" {
		out(" email=%s", r.Email)
	}
	if r.AbuseHoldAt != nil {
		out(" abuse_hold=%s held_at=%s", r.AbuseHoldReason, r.AbuseHoldAt.UTC().Format(time.RFC3339))
	}
	out("\n")
	if s := r.Signup; s != nil {
		out("signup method=%s country=%s asn=%d asn_org=%q email_domain=%s disposable=%t at=%s",
			s.Method, dash(s.Country), s.ASN, s.ASNOrg, s.EmailDomain, s.DisposableEmail, s.CreatedAt.UTC().Format(time.RFC3339))
		if s.SignupIP != "" {
			out(" ip=%s user_agent=%q", s.SignupIP, s.UserAgent)
		}
		if s.ReducedAt != nil {
			out(" address_cleared_at=%s", s.ReducedAt.UTC().Format(time.RFC3339))
		}
		out("\n")
	} else {
		out("signup none (account predates sign-up records)\n")
	}
	for _, l := range r.LinksToHeld {
		out("resembles held_account=%s held_reason=%s signal=%s detected_at=%s%s\n",
			l.HeldAccountID, l.HeldReason, l.Signal, l.DetectedAt.UTC().Format(time.RFC3339), orgSuffix(l.HeldOrgID))
	}
	for _, l := range r.LinkedAccounts {
		out("linked_account=%s signal=%s detected_at=%s%s\n", l.AccountID, l.Signal, l.DetectedAt.UTC().Format(time.RFC3339), orgSuffix(l.HeldOrgID))
	}
	for _, s := range r.Strikes {
		out("strike reason=%s app=%s at=%s\n", s.Reason, dash(s.AppID), s.StruckAt.UTC().Format(time.RFC3339))
	}
	for _, e := range r.AbuseEvents {
		out("event at=%s kind=%s actor=%s subject=%s data=%s\n", e.At.UTC().Format(time.RFC3339), e.Kind, e.Actor, e.Subject, compactJSON(e.Data))
	}
	out("summary links_to_held=%d linked_accounts=%d strikes=%d abuse_events=%d\n",
		len(r.LinksToHeld), len(r.LinkedAccounts), len(r.Strikes), len(r.AbuseEvents))
}

// cmdAccountsHeldLinks is `gregalectl accounts held-links`: new accounts
// linked to abuse-held accounts in the last --since, for the daily review.
func cmdAccountsHeldLinks(args []string) int {
	const label = "accounts held-links"
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	since := fs.String("since", "24h", "window, a duration up to 720h")
	limit := fs.Int("limit", 0, "maximum rows (default 200, max 1000)")
	if !parseOperatorFlags(fs, args) {
		return 2
	}
	d, err := time.ParseDuration(strings.TrimSpace(*since))
	if err != nil || d <= 0 || d > 720*time.Hour {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --since must be a duration like 24h, at most 720h\n", label)
		return 2
	}
	if *limit < 0 || *limit > 1000 {
		_, _ = fmt.Fprintf(osStderr, "gregalectl %s: --limit must be 1..1000\n", label)
		return 2
	}
	query := url.Values{"since": {d.String()}}
	if *limit > 0 {
		query.Set("limit", strconv.Itoa(*limit))
	}
	var r api.HeldLinksResponse
	if code := operatorAPIRequest(label, http.MethodGet, "/v1/admin/held-links?"+query.Encode(), nil, &r, "", false); code != 0 {
		return code
	}
	if jsonEnabled() {
		return emitOperatorJSON(r)
	}
	for _, l := range r.Links {
		_, _ = fmt.Fprintf(osStdout, "detected_at=%s account_id=%s held_account=%s held_reason=%s signal=%s country=%s email_domain=%s disposable=%t%s\n",
			l.DetectedAt.UTC().Format(time.RFC3339), l.AccountID, l.HeldAccountID, l.HeldReason, l.Signal,
			dash(l.Country), dash(l.EmailDomain), l.DisposableEmail, orgSuffix(l.HeldOrgID))
	}
	if len(r.Links) == 0 {
		_, _ = fmt.Fprintf(osStdout, "no held-account links in the last %s\n", d)
	}
	if r.Truncated {
		_, _ = fmt.Fprintln(osStdout, "truncated=true (narrow --since or raise --limit to see more)")
	}
	return 0
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func orgSuffix(orgID string) string {
	if orgID == "" {
		return ""
	}
	return " held_org=" + orgID
}

func compactJSON(raw []byte) string {
	s := strings.Join(strings.Fields(string(raw)), " ")
	if s == "" {
		return "{}"
	}
	if len(s) > 300 {
		return s[:297] + "..."
	}
	return s
}
