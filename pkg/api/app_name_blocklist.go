package api

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// CodeAppNameUnavailable (409): app_name_blocklist refuses the requested app
// name as a new name. Clients treat it like a taken name: a deploy takes a
// suffixed alternative, everything else asks for another name.
const CodeAppNameUnavailable = "app_name_unavailable"

// AppNameBlocklistConfigKey is the global runtime setting that holds the app
// name blocklist (ParseAppNameBlocklist).
const AppNameBlocklistConfigKey = "app_name_blocklist"

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
