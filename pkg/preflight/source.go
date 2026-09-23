package preflight

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ErrInvalidSource is returned for any input that is not a public github.com
// repository. Callers map it to a 422 with a stable RFC 7807 code.
var ErrInvalidSource = errors.New("preflight: not a github.com repository")

// GitHub's own rules: an owner is at most 39 characters of alphanumerics and
// hyphens and may not begin or end with a hyphen; a repository is at most 100
// characters of alphanumerics, hyphen, underscore and period.
var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

const (
	maxSourceInputBytes = 512
	sshPrefix           = "git@github.com:"
)

var allowedHosts = map[string]bool{"github.com": true, "www.github.com": true}

// ParseSource extracts a repository reference from user input.
//
// This function is the SSRF boundary. Preflight never fetches a URL a caller
// supplied: it reduces the input to an owner and a repository name, rejects
// anything else, and every upstream URL is then built from those two validated
// fields. A hostile input cannot redirect the request because the request is
// never constructed from the input.
func ParseSource(raw string) (Source, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxSourceInputBytes {
		return Source{}, fmt.Errorf("%w: empty or oversized input", ErrInvalidSource)
	}

	path := raw
	switch {
	case strings.HasPrefix(raw, sshPrefix):
		path = strings.TrimPrefix(raw, sshPrefix)
	case strings.Contains(raw, "://"):
		parsed, err := url.Parse(raw)
		if err != nil {
			return Source{}, fmt.Errorf("%w: unparseable URL", ErrInvalidSource)
		}
		if parsed.Scheme != "https" {
			return Source{}, fmt.Errorf("%w: scheme %q is not https", ErrInvalidSource, parsed.Scheme)
		}
		if parsed.User != nil {
			return Source{}, fmt.Errorf("%w: credentials in URL", ErrInvalidSource)
		}
		if !allowedHosts[strings.ToLower(parsed.Host)] {
			return Source{}, fmt.Errorf("%w: host %q", ErrInvalidSource, parsed.Host)
		}
		path = parsed.Path
	}

	segments := splitSegments(path)
	// A repository owner cannot contain a dot, so a leading github.com here is
	// unambiguously the host and never a real owner.
	if len(segments) > 0 && allowedHosts[strings.ToLower(segments[0])] {
		segments = segments[1:]
	}
	if len(segments) < 2 {
		return Source{}, fmt.Errorf("%w: need owner and repository", ErrInvalidSource)
	}

	owner := segments[0]
	repo := strings.TrimSuffix(segments[1], ".git")
	if !ownerPattern.MatchString(owner) {
		return Source{}, fmt.Errorf("%w: invalid owner", ErrInvalidSource)
	}
	if !repoPattern.MatchString(repo) || strings.Trim(repo, ".") == "" {
		return Source{}, fmt.Errorf("%w: invalid repository", ErrInvalidSource)
	}

	source := Source{Owner: owner, Repo: repo}
	if len(segments) >= 4 && segments[2] == "tree" {
		if ref := segments[3]; repoPattern.MatchString(ref) {
			source.Ref = ref
		}
	}
	return source, nil
}

// splitSegments returns the non-empty path segments, or nil if any segment is
// a relative path element. Rejecting "." and ".." wholesale is what keeps
// "owner/repo/../../secrets" from reducing to a valid-looking "owner/repo"
// while still allowing the trailing segments of a /tree/<branch> URL.
func splitSegments(path string) []string {
	var segments []string
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return nil
		}
		segments = append(segments, segment)
	}
	return segments
}
