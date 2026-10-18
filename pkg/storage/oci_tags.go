package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
)

// Keep the existing 8 MiB tag-list budget across all pages. The request
// bound also stops an endpoint that endlessly emits empty pages.
const (
	maxOCITagListBytes = 8 << 20
	maxOCITagListPages = 1000
)

// fetchTags enumerates every page of /v2/<repo>/tags/list. A missing
// repository is empty; failure on any later page invalidates the inventory.
func (o *OCIRegistryStorageBackend) fetchTags(ctx context.Context, repo string) ([]string, error) {
	u := o.registry + "/v2/" + o.fullRepo(repo) + "/tags/list"
	scope := "repository:" + o.fullRepo(repo) + ":pull"
	remaining := int64(maxOCITagListBytes)
	visited := make(map[string]struct{})
	var tags []string
	for page := 0; page < maxOCITagListPages; page++ {
		if _, repeated := visited[u]; repeated {
			return nil, fmt.Errorf("%w: tags pagination repeated a page", ErrIncompleteEnumeration)
		}
		visited[u] = struct{}{}
		pageTags, header, size, err := o.fetchTagsPage(ctx, u, scope, remaining, page == 0)
		if err != nil {
			return nil, err
		}
		remaining -= size
		tags = append(tags, pageTags...)
		next, err := nextRegistryTagsPage(u, header.Values("Link"))
		if err != nil {
			return nil, fmt.Errorf("%w: tags pagination: %w", ErrIncompleteEnumeration, err)
		}
		if next == "" {
			return tags, nil
		}
		u = next
	}
	return nil, fmt.Errorf("%w: tags pagination exceeded %d pages", ErrIncompleteEnumeration, maxOCITagListPages)
}

func (o *OCIRegistryStorageBackend) fetchTagsPage(ctx context.Context, u, scope string, maxBytes int64, firstPage bool) ([]string, http.Header, int64, error) {
	resp, err := o.doBearerRequest(ctx, bearerReqOpts{Method: http.MethodGet, URL: u, Scope: scope})
	if err != nil {
		return nil, nil, 0, fmt.Errorf("get tags: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if firstPage && resp.StatusCode == http.StatusNotFound {
		return nil, nil, 0, nil // repository doesn't exist
	}
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, nil, 0, fmt.Errorf("tags returned %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, nil, 0, fmt.Errorf("read tags: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, nil, 0, fmt.Errorf("%w: tag list exceeds %d bytes", ErrIncompleteEnumeration, maxOCITagListBytes)
	}
	var payload struct {
		Tags []string `json:"tags"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, nil, 0, fmt.Errorf("decode tags: %w", err)
	}
	return payload.Tags, resp.Header, int64(len(body)), nil
}

func nextRegistryTagsPage(current string, headers []string) (string, error) {
	base, err := url.Parse(current)
	if err != nil {
		return "", fmt.Errorf("parse tags URL: %w", err)
	}
	var next string
	for _, value := range headers {
		links, err := splitRegistryLinks(value)
		if err != nil {
			return "", err
		}
		for _, link := range links {
			uri, attributes, found := strings.Cut(link, ">")
			if !found || !strings.HasPrefix(uri, "<") {
				return "", fmt.Errorf("malformed registry Link header")
			}
			_, params, err := mime.ParseMediaType("link" + attributes)
			if err != nil {
				return "", fmt.Errorf("malformed registry Link parameters: %w", err)
			}
			for _, relation := range strings.Fields(params["rel"]) {
				if !strings.EqualFold(relation, "next") {
					continue
				}
				candidate, err := registryTagsPageURL(base, strings.TrimPrefix(uri, "<"))
				if err != nil {
					return "", err
				}
				if next != "" && next != candidate {
					return "", fmt.Errorf("registry returned multiple next pages")
				}
				next = candidate
			}
		}
	}
	return next, nil
}

func registryTagsPageURL(base *url.URL, reference string) (string, error) {
	ref, err := url.Parse(reference)
	if err != nil {
		return "", fmt.Errorf("invalid registry pagination URL: %w", err)
	}
	next := base.ResolveReference(ref)
	// Pagination must not forward a repository bearer to another origin or
	// endpoint. Query cursors remain opaque, preserving order and escaping.
	if !strings.EqualFold(next.Scheme, base.Scheme) || !strings.EqualFold(next.Hostname(), base.Hostname()) || registryURLPort(next) != registryURLPort(base) || next.Path != base.Path || next.User != nil || next.Opaque != "" {
		return "", fmt.Errorf("registry pagination left the tags endpoint")
	}
	next.Fragment, next.RawFragment = "", ""
	return next.String(), nil
}

func registryURLPort(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// Link values are comma-separated except inside a URI reference or a
// quoted parameter. Splitting on every comma can silently lose rel=next.
func splitRegistryLinks(value string) ([]string, error) {
	var links []string
	var inURI, quoted, escaped bool
	start := 0
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch {
		case inURI:
			if c == '>' {
				inURI = false
			}
		case escaped:
			escaped = false
		case quoted:
			switch c {
			case '\\':
				escaped = true
			case '"':
				quoted = false
			}
		case c == '<':
			inURI = true
		case c == '"':
			quoted = true
		case c == ',':
			if link := strings.TrimSpace(value[start:i]); link != "" {
				links = append(links, link)
			}
			start = i + 1
		}
	}
	if inURI || quoted {
		return nil, fmt.Errorf("unterminated registry Link header")
	}
	if link := strings.TrimSpace(value[start:]); link != "" {
		links = append(links, link)
	}
	return links, nil
}
