package api

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type IssueSavedViewFilters struct {
	Team         string `json:"team,omitempty"`
	State        string `json:"state,omitempty"`
	Environment  string `json:"environment,omitempty"`
	Assignee     string `json:"assignee,omitempty"`
	Sort         string `json:"sort,omitempty"`
	MinCustomers int64  `json:"min_customers,omitempty"`
	Tag          string `json:"tag,omitempty"`
}

type PutIssueSavedViewRequest struct {
	Name    string                `json:"name"`
	Filters IssueSavedViewFilters `json:"filters"`
}

type IssueSavedView struct {
	Key                string                `json:"key"`
	Name               string                `json:"name"`
	Filters            IssueSavedViewFilters `json:"filters"`
	CreatedAt          time.Time             `json:"created_at"`
	UpdatedAt          time.Time             `json:"updated_at"`
	UpdatedByAccountID string                `json:"updated_by_account_id,omitempty"`
}

type ListIssueSavedViewsResponse struct {
	Items []IssueSavedView `json:"items"`
}

var issueViewKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
var issueViewEnvironmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func ValidateIssueSavedViewKey(key string) error {
	if len(key) > IssueSavedViewKeyMaxBytes || !issueViewKeyPattern.MatchString(key) {
		return fmt.Errorf("view key must contain 1–%d lowercase letters, digits, or hyphens and start with a letter or digit", IssueSavedViewKeyMaxBytes)
	}
	return nil
}

func NormalizeIssueSavedView(in PutIssueSavedViewRequest) (PutIssueSavedViewRequest, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > IssueSavedViewNameMaxBytes || !utf8.ValidString(in.Name) || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		return in, fmt.Errorf("view name must contain 1–%d UTF-8 bytes without control characters", IssueSavedViewNameMaxBytes)
	}
	f := &in.Filters
	if f.State != "" && f.State != "open" && f.State != "resolved" && f.State != "ignored" {
		return in, fmt.Errorf("invalid issue state")
	}
	if f.Environment != "" && !issueViewEnvironmentPattern.MatchString(f.Environment) {
		return in, fmt.Errorf("invalid issue environment")
	}
	f.Assignee = strings.TrimSpace(f.Assignee)
	switch f.Assignee {
	case "", "me", "unassigned":
	default:
		id, err := uuid.Parse(f.Assignee)
		if err != nil || id == uuid.Nil {
			return in, fmt.Errorf("assignee must be me, unassigned, or a nonzero account UUID")
		}
		f.Assignee = id.String()
	}
	if f.Team != "" {
		_, team, err := NormalizeIssueAssignment(f.Assignee, f.Team)
		if err != nil {
			return in, err
		}
		f.Team = team
	}
	if f.Sort == "" {
		f.Sort = "recent"
	}
	if f.Sort != "recent" && f.Sort != "impact" || f.MinCustomers < 0 {
		return in, fmt.Errorf("invalid issue ordering or customer threshold")
	}
	if _, err := ParseIssueTagFilter(f.Tag); err != nil {
		return in, err
	}
	return in, nil
}

// Query applies an exact saved filter set and starts a fresh page. "me" remains
// a viewer-relative token; the authenticated list handler resolves its identity.
func (f IssueSavedViewFilters) Query() url.Values {
	return url.Values{"team": {f.Team}, "state": {f.State}, "environment": {f.Environment}, "assignee": {f.Assignee}, "sort": {f.Sort}, "min_customers": {strconv.FormatInt(f.MinCustomers, 10)}, "tag": {f.Tag}}
}

// NormalizeIssueSourcePathPrefix accepts bounded local paths only. Source
// routing uses lexical path boundaries and never opens a file or evaluates globs.
func NormalizeIssueSourcePathPrefix(raw string) (string, error) {
	return normalizeIssueSourcePath(raw, IssueMaxTypeBytes)
}

// NormalizeIssueSourceFilePath accepts the wider occurrence-frame bound.
func NormalizeIssueSourceFilePath(raw string) (string, error) {
	return normalizeIssueSourcePath(strings.TrimPrefix(raw, "file://"), IssueMaxFrameBytes)
}

func normalizeIssueSourcePath(raw string, maxBytes int) (string, error) {
	value := strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/")
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.ContainsAny(value, "?#") || strings.Contains(value, "://") {
		return "", fmt.Errorf("source_path_prefix must be a bounded local source path")
	}
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return "", fmt.Errorf("source_path_prefix must not contain parent traversal")
		}
	}
	value = path.Clean(value)
	if value == "." {
		return "", fmt.Errorf("source_path_prefix must select a source path")
	}
	return value, nil
}
