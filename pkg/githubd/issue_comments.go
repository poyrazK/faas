package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// Marker comments for agent deliveries (ADR-20261027090010 §8). One comment per
// marker: the comment the App wrote with that marker is found and updated,
// or a new one is created. Every write is preceded by a read of the issue,
// so repeating a write whose answer was lost reconciles against GitHub
// instead of adding a second comment.

const (
	// issueCommentBodyMaxBytes is GitHub's comment limit; the marker counts.
	issueCommentBodyMaxBytes = 65_536
	// issueCommentPageSize and issueCommentPagesMax bound the search for
	// the marker: up to 2,000 comments.
	issueCommentPageSize = 100
	issueCommentPagesMax = 20
	// issueCommentPageMaxBytes bounds one page of comments, each up to the
	// comment limit.
	issueCommentPageMaxBytes = 8 << 20
)

var issueCommentMarkerKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)

// IssueCommentClient writes marker comments with an installation's token.
type IssueCommentClient interface {
	UpsertIssueComment(ctx context.Context, installation int64, repository string, number int64, markerKey, body string) (githubdgrpc.IssueComment, error)
}

type httpIssueComments struct {
	tokens *TokenCache
	client HTTPClient
}

// NewHTTPIssueComments writes marker comments through the GitHub REST API.
func NewHTTPIssueComments(tokens *TokenCache, client HTTPClient) IssueCommentClient {
	if client == nil {
		client = NewHTTPClient()
	}
	return &httpIssueComments{tokens: tokens, client: client}
}

// UpsertIssueComment checks that the installation is the account's before
// any GitHub call.
func (s *RealService) UpsertIssueComment(ctx context.Context, account string, installation int64, repository string, number int64, markerKey, body string) (githubdgrpc.IssueComment, error) {
	if account == "" || installation <= 0 || !validCIPullRequestInput(repository, number) || !issueCommentMarkerKey.MatchString(markerKey) ||
		strings.TrimSpace(body) == "" || len(issueCommentBody(markerKey, body)) > issueCommentBodyMaxBytes {
		return githubdgrpc.IssueComment{}, status.Error(codes.InvalidArgument, "githubd: an issue comment needs an account, installation, repository, number, marker and a body within GitHub's limit")
	}
	inst, err := s.installForAccount(ctx, account, installation)
	if errors.Is(err, state.ErrNotFound) || err == nil && (inst.AccountID != account || inst.InstallationID != installation) {
		return githubdgrpc.IssueComment{}, status.Error(codes.NotFound, "githubd: the account has no such GitHub installation")
	}
	if err != nil || s.IssueComments == nil {
		return githubdgrpc.IssueComment{}, status.Error(codes.Unavailable, "githubd: issue comments are unavailable")
	}
	return s.IssueComments.UpsertIssueComment(ctx, installation, repository, number, markerKey, body)
}

// UpsertIssueComment forwards the optional comment RPC.
func (s *RecoveryService) UpsertIssueComment(ctx context.Context, account string, installation int64, repository string, number int64, markerKey, body string) (githubdgrpc.IssueComment, error) {
	service, ok := s.Service.(githubdgrpc.IssueCommentService)
	if !ok {
		return githubdgrpc.IssueComment{}, status.Error(codes.Unimplemented, "githubd: issue comments are not configured")
	}
	return service.UpsertIssueComment(ctx, account, installation, repository, number, markerKey, body)
}

// issueCommentBody is the comment as written: the body, then the hidden
// marker.
func issueCommentBody(markerKey, body string) string {
	return body + "\n\n" + issueCommentMarker(markerKey)
}

func issueCommentMarker(markerKey string) string { return "<!-- gregale:" + markerKey + " -->" }

type gitHubIssueComment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    struct {
		Type string `json:"type"`
	} `json:"user"`
}

func (c *httpIssueComments) UpsertIssueComment(ctx context.Context, installation int64, repository string, number int64, markerKey, body string) (githubdgrpc.IssueComment, error) {
	token, err := c.tokens.Token(ctx, installation)
	if err != nil {
		return githubdgrpc.IssueComment{}, status.Error(codes.Unavailable, "githubd: no installation token for the comment")
	}
	owner, repo, _ := strings.Cut(repository, "/")
	base := fmt.Sprintf("%s/repos/%s/%s/issues", GitHubAPI, url.PathEscape(owner), url.PathEscape(repo))
	full, marker := issueCommentBody(markerKey, body), issueCommentMarker(markerKey)
	existing, err := c.find(ctx, token, base, number, marker)
	if err != nil {
		return githubdgrpc.IssueComment{}, err
	}
	method, endpoint, want := http.MethodPost, fmt.Sprintf("%s/%d/comments", base, number), http.StatusCreated
	if existing != nil {
		if existing.Body == full {
			return githubdgrpc.IssueComment{ID: existing.ID, URL: existing.HTMLURL}, nil
		}
		method, endpoint, want = http.MethodPatch, fmt.Sprintf("%s/comments/%d", base, existing.ID), http.StatusOK
	}
	written, sent, err := c.write(ctx, token, method, endpoint, full, want)
	if err == nil {
		return githubdgrpc.IssueComment{ID: written.ID, URL: written.HTMLURL, Created: existing == nil}, nil
	}
	if !sent {
		return githubdgrpc.IssueComment{}, err
	}
	// The write may have landed: read the issue again before answering.
	confirmed, ferr := c.find(ctx, token, base, number, marker)
	switch {
	case ferr != nil:
		return githubdgrpc.IssueComment{}, status.Error(codes.Unknown, "githubd: the comment write's answer was lost and could not be confirmed")
	case confirmed != nil && confirmed.Body == full:
		return githubdgrpc.IssueComment{ID: confirmed.ID, URL: confirmed.HTMLURL, Created: existing == nil}, nil
	default:
		return githubdgrpc.IssueComment{}, status.Error(codes.Unavailable, "githubd: the comment write did not land")
	}
}

// find returns the App's comment carrying marker, or nil. Only comments a
// bot wrote count, so a person quoting the marker does not capture it.
func (c *httpIssueComments) find(ctx context.Context, token, base string, number int64, marker string) (*gitHubIssueComment, error) {
	for page := 1; page <= issueCommentPagesMax; page++ {
		endpoint := fmt.Sprintf("%s/%d/comments?per_page=%d&page=%d", base, number, issueCommentPageSize, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, "githubd: invalid comment listing request")
		}
		setGitHubHeaders(req, token)
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, status.Error(codes.Unavailable, "githubd: listing the issue's comments failed")
		}
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, issueCommentPageMaxBytes+1))
		_ = resp.Body.Close()
		if err := issueCommentStatus(resp, "listing the issue's comments"); err != nil {
			return nil, err
		}
		var comments []gitHubIssueComment
		if rerr != nil || len(raw) > issueCommentPageMaxBytes || json.Unmarshal(raw, &comments) != nil {
			return nil, status.Error(codes.Unavailable, "githubd: GitHub returned an unreadable comment listing")
		}
		for i := range comments {
			if comments[i].ID > 0 && comments[i].User.Type == "Bot" && strings.Contains(comments[i].Body, marker) {
				return &comments[i], nil
			}
		}
		if len(comments) < issueCommentPageSize {
			return nil, nil
		}
	}
	return nil, nil
}

// write sends one comment write. sent reports whether GitHub may have
// applied it: the request left and no definite refusal came back.
func (c *httpIssueComments) write(ctx context.Context, token, method, endpoint, body string, want int) (gitHubIssueComment, bool, error) {
	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
	if err != nil {
		return gitHubIssueComment{}, false, status.Error(codes.InvalidArgument, "githubd: encode comment")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return gitHubIssueComment{}, false, status.Error(codes.InvalidArgument, "githubd: invalid comment request")
	}
	setGitHubHeaders(req, token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return gitHubIssueComment{}, true, status.Error(codes.Unknown, "githubd: the comment write's answer was lost")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == want {
		var written gitHubIssueComment
		if err := decodeGitHubJSON(resp.Body, &written); err != nil || written.ID <= 0 {
			return gitHubIssueComment{}, true, status.Error(codes.Unknown, "githubd: GitHub's answer to the comment write was unreadable")
		}
		return written, true, nil
	}
	err = issueCommentStatus(resp, "writing the comment")
	if err == nil {
		err = status.Errorf(codes.Unknown, "githubd: writing the comment: unexpected status %d", resp.StatusCode)
	}
	// A server error may follow an applied write; a refusal or a rate limit
	// does not.
	return gitHubIssueComment{}, resp.StatusCode >= 500, err
}

// issueCommentStatus maps a GitHub answer to a gRPC status: refusals are
// FailedPrecondition, rate limits and server errors Unavailable.
func issueCommentStatus(resp *http.Response, op string) error {
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return nil
	case code == http.StatusTooManyRequests || code == http.StatusRequestTimeout || code >= 500,
		code == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0",
		code == http.StatusForbidden && resp.Header.Get("Retry-After") != "":
		return status.Errorf(codes.Unavailable, "githubd: %s: GitHub answered %d", op, code)
	default:
		return status.Errorf(codes.FailedPrecondition, "githubd: %s: GitHub refused with %d", op, code)
	}
}
