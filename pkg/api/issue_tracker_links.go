package api

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type PutIssueTrackerLinkRequest struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}
type IssueTrackerLink struct {
	ID                 string    `json:"id"`
	OriginalIssueID    string    `json:"original_issue_id"`
	Provider           string    `json:"provider"`
	ExternalKey        string    `json:"external_key"`
	URL                string    `json:"url"`
	Title              string    `json:"title,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	CreatedByAccountID string    `json:"created_by_account_id,omitempty"`
	UpdatedByAccountID string    `json:"updated_by_account_id,omitempty"`
}
type ListIssueTrackerLinksResponse struct {
	Items      []IssueTrackerLink `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
type NormalizedIssueTrackerLink struct {
	PutIssueTrackerLinkRequest
	Provider, ExternalKey string
}

var trackerGithubOwner = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)
var trackerGithubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
var trackerLinearWorkspace = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,62}$`)
var trackerLinearKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,31}-[1-9][0-9]{0,18}$`)

// NormalizeIssueTrackerLink canonicalizes public GitHub issue and Linear issue
// URLs. It never fetches a URL, validates provider access, or creates a ticket.
func NormalizeIssueTrackerLink(in PutIssueTrackerLinkRequest) (NormalizedIssueTrackerLink, error) {
	out := NormalizedIssueTrackerLink{PutIssueTrackerLinkRequest: in}
	out.URL = strings.TrimSpace(in.URL)
	out.Title = strings.TrimSpace(in.Title)
	for _, field := range []struct {
		value string
		max   int
	}{{out.URL, IssueTrackerLinkURLMaxBytes}, {out.Title, IssueTrackerLinkTitleMaxBytes}} {
		if len(field.value) > field.max || !utf8.ValidString(field.value) || strings.IndexFunc(field.value, unicode.IsControl) >= 0 {
			return out, fmt.Errorf("tracker URL or title is invalid or oversized")
		}
	}
	u, err := url.Parse(out.URL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.RawPath != "" {
		return out, fmt.Errorf("use an HTTPS GitHub or Linear issue URL without credentials or encoded path separators")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch strings.ToLower(u.Host) {
	case "github.com":
		if len(parts) != 4 || !trackerGithubOwner.MatchString(parts[0]) || !trackerGithubRepo.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." || parts[2] != "issues" {
			return out, fmt.Errorf("use https://github.com/OWNER/REPO/issues/NUMBER")
		}
		n, err := strconv.ParseUint(parts[3], 10, 64)
		if err != nil || n == 0 || strings.HasPrefix(parts[3], "+") {
			return out, fmt.Errorf("GitHub issue number must be positive")
		}
		owner, repo, number := strings.ToLower(parts[0]), strings.ToLower(parts[1]), strconv.FormatUint(n, 10)
		out.Provider = "github"
		out.ExternalKey = owner + "/" + repo + "#" + number
		out.URL = "https://github.com/" + owner + "/" + repo + "/issues/" + number
	case "linear.app":
		if len(parts) != 3 && len(parts) != 4 || !trackerLinearWorkspace.MatchString(parts[0]) || parts[1] != "issue" || !trackerLinearKey.MatchString(parts[2]) {
			return out, fmt.Errorf("use https://linear.app/WORKSPACE/issue/TEAM-NUMBER")
		}
		workspace, key := strings.ToLower(parts[0]), strings.ToUpper(parts[2])
		out.Provider = "linear"
		out.ExternalKey = workspace + "/" + key
		out.URL = "https://linear.app/" + workspace + "/issue/" + key
	default:
		return out, fmt.Errorf("supported tracker hosts are github.com and linear.app")
	}
	return out, nil
}
