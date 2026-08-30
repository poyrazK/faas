package main

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// validateGitHubRef accepts branch/tag refs, including the conventional
// slash-separated form such as release/2026-q3, while rejecting characters
// that would change curl's URL semantics or create a path traversal segment.
func validateGitHubRef(ref string) error {
	if ref == "" || len(ref) > 200 {
		return fmt.Errorf("invalid git ref %q", ref)
	}
	for _, r := range ref {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune(`\?#[\]{}<>"'`, r) {
			return fmt.Errorf("invalid character %q in git ref", string(r))
		}
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return fmt.Errorf("invalid path segment in git ref %q", ref)
		}
	}
	return nil
}

// githubTarballURL builds the GitHub archive URL from encoded path segments.
// Repo validation prevents an extra repository path from being injected;
// segment-by-segment ref escaping preserves valid ref slashes without allowing
// query or fragment delimiters to escape into curl's command-line argument.
func githubTarballURL(repoFullName, ref string) (string, error) {
	if err := validateRepoSlug(repoFullName); err != nil {
		return "", err
	}
	if err := validateGitHubRef(ref); err != nil {
		return "", err
	}

	repoParts := strings.Split(repoFullName, "/")
	for i := range repoParts {
		repoParts[i] = url.PathEscape(repoParts[i])
	}
	refParts := strings.Split(ref, "/")
	for i := range refParts {
		refParts[i] = url.PathEscape(refParts[i])
	}
	return "https://api.github.com/repos/" + strings.Join(repoParts, "/") + "/tarball/" + strings.Join(refParts, "/"), nil
}
