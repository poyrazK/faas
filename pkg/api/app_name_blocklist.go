package api

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
)

// App name blocklist bounds.
const (
	AppNameBlocklistMaxEntries  = 5000
	AppNameBlocklistMaxEntryLen = 63
)

// ParseAppNameBlocklist reads the app_name_blocklist runtime setting: entries
// separated by commas, spaces or newlines. An entry is an exact app name
// ("paypal") or a pattern where * matches any run of characters
// ("*-login", "*paypal*"). Entries are lowercased; an entry may use only
// lowercase letters, digits, hyphens and *, and must name at least one
// character other than * and -.
func ParseAppNameBlocklist(raw string) ([]string, error) {
	fields := strings.FieldsFunc(strings.ToLower(raw), func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\r' || r == '\t'
	})
	if len(fields) > AppNameBlocklistMaxEntries {
		return nil, fmt.Errorf("at most %d entries", AppNameBlocklistMaxEntries)
	}
	out := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, entry := range fields {
		if len(entry) > AppNameBlocklistMaxEntryLen {
			return nil, fmt.Errorf("entry %q is longer than %d characters", entry, AppNameBlocklistMaxEntryLen)
		}
		literal := false
		for _, r := range entry {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
				literal = true
			case r == '-' || r == '*':
			default:
				return nil, fmt.Errorf("entry %q may use only lowercase letters, digits, hyphens and *", entry)
			}
		}
		if !literal {
			return nil, fmt.Errorf("entry %q must name at least one letter or digit", entry)
		}
		if _, dup := seen[entry]; dup {
			continue
		}
		seen[entry] = struct{}{}
		out = append(out, entry)
	}
	return out, nil
}

// MatchAppNameBlocklist returns the first entry of blocklist that slug
// matches, and whether one did.
func MatchAppNameBlocklist(slug string, blocklist []string) (string, bool) {
	for _, entry := range blocklist {
		if !strings.Contains(entry, "*") {
			if entry == slug {
				return entry, true
			}
			continue
		}
		// Entries hold only [a-z0-9-*], so * is path.Match's only
		// metacharacter here, and slugs never contain '/'.
		if ok, err := path.Match(entry, slug); err == nil && ok {
			return entry, true
		}
	}
	return "", false
}

// appNameBlocklistSource supplies the live blocklist; apid points it at its
// runtime config (SetAppNameBlocklistSource). Unset means no blocklist.
var appNameBlocklistSource atomic.Pointer[func() []string]

// SetAppNameBlocklistSource sets where BlockedAppName reads the blocklist.
func SetAppNameBlocklistSource(source func() []string) {
	if source == nil {
		appNameBlocklistSource.Store(nil)
		return
	}
	appNameBlocklistSource.Store(&source)
}

// BlockedAppName reports whether the live blocklist refuses slug as a new
// app name, and the entry that matched. Like the reserved names it binds only
// allocation (create, rename, a new project workload): an app that already
// has the name keeps it.
func BlockedAppName(slug string) (string, bool) {
	source := appNameBlocklistSource.Load()
	if source == nil {
		return "", false
	}
	return MatchAppNameBlocklist(slug, (*source)())
}

// AppNameReportResponse is GET /v1/admin/app-names/report: the existing apps
// whose names an app name blocklist matches. source is "live"
// (app_name_blocklist) or "preview" (the request's ?blocklist=). Nothing is
// changed; truncated means more apps matched than were listed.
type AppNameReportResponse struct {
	Source    string               `json:"source"`
	Entries   int                  `json:"entries"`
	Matches   []AppNameReportMatch `json:"matches"`
	Truncated bool                 `json:"truncated"`
}

// AppNameReportMatch is one existing app a blocklist entry matches.
type AppNameReportMatch struct {
	Slug      string `json:"slug"`
	AppID     string `json:"app_id"`
	AccountID string `json:"account_id"`
	OrgID     string `json:"org_id,omitempty"`
	Status    string `json:"status"`
	Entry     string `json:"entry"`
}

// GetAppNameReport lists existing apps the live app_name_blocklist matches,
// or, with blocklist set, the ones that list would match (operator-only).
func (c *Client) GetAppNameReport(ctx context.Context, blocklist string) (AppNameReportResponse, error) {
	path := "/v1/admin/app-names/report"
	if blocklist = strings.TrimSpace(blocklist); blocklist != "" {
		path += "?" + url.Values{"blocklist": {blocklist}}.Encode()
	}
	var out AppNameReportResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}
