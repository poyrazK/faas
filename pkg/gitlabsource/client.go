// Package gitlabsource reads immutable repository content from GitLab.com.
// Authentication and token storage belong to the caller; this package never
// persists or logs OAuth credentials.
package gitlabsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const API = "https://gitlab.com/api/v4"

var ErrUnavailable = errors.New("gitlab source unavailable")

type Client struct {
	HTTP *http.Client
	// APIURL is for isolated tests. Production callers leave it empty.
	APIURL string
}

type Repository struct {
	ID         int64  `json:"id"`
	Path       string `json:"path_with_namespace"`
	Visibility string `json:"visibility"`
	Archived   bool   `json:"archived"`
}

func (c Client) endpoint(path string) string {
	if c.APIURL != "" {
		return strings.TrimRight(c.APIURL, "/") + path
	}
	return API + path
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		copy := *c.HTTP
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if copy.Timeout == 0 || copy.Timeout > 30*time.Second {
			copy.Timeout = 30 * time.Second
		}
		return &copy
	}
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (c Client) get(ctx context.Context, token, path string) (*http.Response, error) {
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return nil, ErrUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint(path), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed", ErrUnavailable)
	}
	if response == nil || response.Body == nil {
		return nil, ErrUnavailable
	}
	// Redirects are rejected even with an injected HTTP client: the bearer
	// token must never be sent to another origin.
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, response.StatusCode)
	}
	return response, nil
}

func ValidRepositoryPath(path string) bool {
	if path == "" || strings.TrimSpace(path) != path || len(path) > 512 || strings.ContainsAny(path, "?#@\\\x00") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." || strings.HasSuffix(part, ".git") {
			return false
		}
		for _, char := range part {
			if char != '-' && char != '_' && char != '.' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
				return false
			}
		}
	}
	return strings.Contains(path, "/")
}

// VerifyRepository resolves a path through GitLab's API, checking the
// provider-assigned ID as well as the canonical namespace path. A rename or
// account-token access change therefore fails closed before content is read.
func (c Client) VerifyRepository(ctx context.Context, token, path string, expectedID int64) (Repository, error) {
	if !ValidRepositoryPath(path) {
		return Repository{}, ErrUnavailable
	}
	response, err := c.get(ctx, token, "/projects/"+url.PathEscape(path))
	if err != nil {
		return Repository{}, err
	}
	defer func() { _ = response.Body.Close() }()
	var repository Repository
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&repository) != nil || repository.ID <= 0 || repository.Path != path ||
		(expectedID > 0 && repository.ID != expectedID) || repository.Archived {
		return Repository{}, ErrUnavailable
	}
	return repository, nil
}

func validSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, char := range sha {
		if char < '0' || char > '9' && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// ResolveCommit returns the immutable full SHA for a branch, tag, or commit.
func (c Client) ResolveCommit(ctx context.Context, token string, repositoryID int64, ref string) (string, error) {
	if repositoryID <= 0 || ref == "" || len(ref) > 512 || strings.ContainsAny(ref, "?#\\\x00\r\n") {
		return "", ErrUnavailable
	}
	endpoint := "/projects/" + strconv.FormatInt(repositoryID, 10) + "/repository/"
	var commit struct {
		ID string `json:"id"`
	}
	var named struct {
		Commit *struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	var target any = &commit
	exactRef := validSHA(ref)
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		ref = strings.TrimPrefix(ref, "refs/heads/")
		endpoint += "branches/" + url.PathEscape(ref)
		target = &named
	case strings.HasPrefix(ref, "refs/tags/"):
		ref = strings.TrimPrefix(ref, "refs/tags/")
		endpoint += "tags/" + url.PathEscape(ref)
		target = &named
	case validSHA(ref):
		endpoint += "commits/" + ref
	default:
		return "", ErrUnavailable
	}
	response, err := c.get(ctx, token, endpoint)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target) != nil {
		return "", ErrUnavailable
	}
	if target == &named {
		if named.Commit == nil {
			return "", ErrUnavailable
		}
		commit.ID = named.Commit.ID
	}
	if !validSHA(commit.ID) || exactRef && commit.ID != ref {
		return "", ErrUnavailable
	}
	return commit.ID, nil
}

// Archive downloads the complete tree at one verified commit. The caller
// must close the returned stream and enforce maxBytes while parsing it.
func (c Client) Archive(ctx context.Context, token string, repositoryID int64, sha string, maxBytes int64) (io.ReadCloser, error) {
	if repositoryID <= 0 || !validSHA(sha) || maxBytes <= 0 {
		return nil, ErrUnavailable
	}
	path := "/projects/" + strconv.FormatInt(repositoryID, 10) + "/repository/archive.tar.gz?sha=" + sha + "&include_lfs_blobs=false"
	response, err := c.get(ctx, token, path)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > maxBytes {
		_ = response.Body.Close()
		return nil, ErrUnavailable
	}
	return response.Body, nil
}
