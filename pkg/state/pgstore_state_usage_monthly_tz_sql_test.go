// SQL-shape regression for the TZ normalisation in UsageByMonth and
// CurrentMonthOverageCents. Companion to
// pgstore_usage_by_hour_tz_sql_test.go, which pins the same property
// for UsageByHour / UsageByAccount.
//
// Background (memory: pkg-state-usage-monthly-tz-compare): every
// `date_trunc('month', <timestamptz>)` in pgstore.go is session-TZ
// dependent. A Postgres session whose TimeZone is e.g.
// `Europe/Istanbul` (UTC+03 with no DST) renders the result as a
// timestamptz that is 3 hours earlier in UTC than the literal the
// caller meant. The previous shape of UsageByMonth
// (`date_trunc('month', $2::timestamptz) = month` against the
// `usage_monthly` view) compared in this TZ-shifted space, and a
// caller passing `2026-12-01T00:00:00Z` got back no rows even when
// December rows existed. The fix bypassed the view and queried
// usage_minutes directly with `minute >= $2 AND minute < $3` (both
// bound in UTC, pre-computed in Go).
//
// CurrentMonthOverageCents had the same bug at `date_trunc('month',
// now())`. The fix binds a Go-pre-computed UTC monthStart as $2
// instead.
//
// The unit test below reads pgstore.go via //go:embed and asserts
// the fixed shape: the buggy substrings must NOT appear, and the
// fixed substrings must. A future refactor that re-introduces
// session-TZ-dependent bucketing will fail here at unit-test time,
// well before a misconfigured session silently mis-buckets rows.

package state

import (
	_ "embed"
	"strings"
	"testing"
)

//go:embed pgstore.go
var pgStoreUsageMonthlySource string

//go:embed queries.sql
var invoiceQueriesSource string

//go:embed pgstore_invoice_details.go
var invoiceStoreSource string

// TestUsageByMonth_DoesNotUseSessionTZDateTrunc pins that the
// buggy form `date_trunc('month', $N::timestamptz)` (which is
// session-TZ-dependent) does NOT appear in the UsageByMonth SQL.
// The fixed form queries `usage_minutes` directly with a half-open
// UTC range — see body.
func TestUsageByMonth_DoesNotUseSessionTZDateTrunc(t *testing.T) {
	body := extractFn(pgStoreUsageMonthlySource, "func (s *PgStore) UsageByMonth(")
	if body == "" {
		t.Fatal("could not locate UsageByMonth in pgstore.go")
	}
	sqlOnly := stripSQLStaticGuardComments(body)
	// The buggy substrings must NOT appear. Each banned token is
	// specific enough to be unambiguous in SQL — none of them
	// appear as a substring of a Go variable assignment. The
	// previous draft's `= month` was tripping on `:= monthStart`
	// in Go, not on SQL, so we use the more specific predicate
	// `usage_monthly where` (the view-backed buggy form was
	// `select ... from usage_monthly where ... date_trunc(...)
	// = month`).
	for _, banned := range []string{
		"date_trunc('month', $2::timestamptz)",
		"from usage_monthly",
	} {
		if strings.Contains(sqlOnly, banned) {
			t.Errorf("UsageByMonth SQL still contains %q — re-introduces session-TZ dependence (memory: pkg-state-usage-monthly-tz-compare). SQL-only body:\n%s",
				banned, sqlOnly)
		}
	}
	// The fixed substrings must appear.
	for _, required := range []string{
		"from usage_minutes",
		"minute >= $2",
		"minute <  $3",
	} {
		if !strings.Contains(sqlOnly, required) {
			t.Errorf("UsageByMonth SQL missing required fragment %q — fix is incomplete. SQL-only body:\n%s",
				required, sqlOnly)
		}
	}
}

// TestCurrentMonthOverageCents_DoesNotUseSessionTZDateTrunc pins
// that the buggy form `date_trunc('month', now())` does NOT appear
// in CurrentMonthOverageCents. The fixed form binds a Go-pre-
// computed UTC monthStart as $2.
func TestCurrentMonthOverageCents_DoesNotUseSessionTZDateTrunc(t *testing.T) {
	body := extractFn(pgStoreUsageMonthlySource, "func (s *PgStore) CurrentMonthOverageCents(")
	if body == "" {
		t.Fatal("could not locate CurrentMonthOverageCents in pgstore.go")
	}
	sqlOnly := stripSQLStaticGuardComments(body)
	if strings.Contains(sqlOnly, "date_trunc('month', now())") {
		t.Errorf("CurrentMonthOverageCents SQL still uses session-TZ-dependent `date_trunc('month', now())` — re-introduces the bug fixed by this PR. SQL-only body:\n%s", sqlOnly)
	}
	// Belt-and-braces: any `date_trunc('month', ...)` in this body
	// is suspect, since CurrentMonthOverageCents has no other use
	// for it. The fix binds `$2` directly.
	for _, line := range strings.Split(sqlOnly, "\n") {
		if strings.Contains(line, "date_trunc('month'") {
			t.Errorf("CurrentMonthOverageCents has a date_trunc('month', ...) — drop it and bind a Go-pre-computed UTC monthStart instead. Line: %q", strings.TrimSpace(line))
		}
	}
	// Required: a $2-bound monthStart comparison.
	if !strings.Contains(sqlOnly, "minute >= $2") {
		t.Errorf("CurrentMonthOverageCents SQL must use `minute >= $2` (Go-pre-computed UTC monthStart); SQL-only body:\n%s", sqlOnly)
	}
}

// TestListInvoicesForAccount_DoesNotUseSessionTZDateTrunc pins
// the same property for ListInvoicesForAccount (both branches).
func TestListInvoicesForAccount_DoesNotUseSessionTZDateTrunc(t *testing.T) {
	body := extractFn(invoiceStoreSource, "func (s *PgStore) ListInvoicesForAccount(")
	if body == "" {
		t.Fatal("could not locate ListInvoicesForAccount in pgstore_invoice_details.go")
	}
	if !strings.Contains(body, "ListInvoiceSnapshots") || !strings.Contains(body, "time.UTC") {
		t.Fatal("invoice adapter must bind UTC boundaries to the generated snapshot query")
	}
	start := strings.Index(invoiceQueriesSource, "-- name: ListInvoiceSnapshots")
	end := strings.Index(invoiceQueriesSource, "-- name: GetInvoiceSnapshot")
	if start < 0 || end < start {
		t.Fatal("invoice snapshot SQL not found")
	}
	sqlOnly := invoiceQueriesSource[start:end]
	if strings.Contains(sqlOnly, "date_trunc('month', $2::timestamptz)") {
		t.Errorf("ListInvoicesForAccount SQL still uses session-TZ-dependent `date_trunc('month', $2::timestamptz)` — re-introduces the bug fixed by this PR. SQL-only body:\n%s", sqlOnly)
	}
	// The fix uses direct comparison against $2/$3 pre-computed
	// in UTC.
	if !strings.Contains(sqlOnly, "period_end >= sqlc.narg(month_start)") || !strings.Contains(sqlOnly, "period_end < sqlc.narg(month_end)") {
		t.Errorf("ListInvoicesForAccount SQL must use direct UTC comparison `period_end >= $2 and period_end < $3`; SQL-only body:\n%s", sqlOnly)
	}
}

// extractFn is defined in sql_static_guard_helpers_test.go so both
// pgstore_usage_by_hour_tz_sql_test.go and this file share one
// implementation.

// stripSQLStaticGuardComments returns only the SQL fragments in
// `body` that come from Go raw-string literals (the
// “ `...` “ form), with `//` comments stripped first. The
// doc-comments on these functions reference the buggy form to
// document the fix (e.g. "The previous shape `minute >=
// date_trunc('month', now())` returned …"); without the comment
// filter, a naive substring check trips on documentation, not on
// real SQL.
//
// The implementation does a single character-by-character scan:
// `//` outside a backtick starts a Go comment that extends to the
// next newline; backticks toggle raw-string-literal mode. Every
// byte INSIDE a backtick is emitted verbatim. Newlines inside
// raw strings are preserved (so multi-line SQL queries come
// through intact). Newlines outside backticks are emitted as
// whitespace so substring searches like `minute >= $2` still
// find matches that span a single source line.
func stripSQLStaticGuardComments(body string) string {
	var sb strings.Builder
	inBacktick := false
	inLineComment := false
	for i := 0; i < len(body); i++ {
		c := body[i]
		if inLineComment {
			if c == '\n' {
				inLineComment = false
				sb.WriteByte('\n')
			}
			continue
		}
		if inBacktick {
			if c == '`' {
				inBacktick = false
				sb.WriteByte('\n')
			} else {
				sb.WriteByte(c)
			}
			continue
		}
		switch c {
		case '`':
			inBacktick = true
		case '/':
			if i+1 < len(body) && body[i+1] == '/' {
				inLineComment = true
				i++ // consume the second '/'
			} else {
				sb.WriteByte(c)
			}
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}
