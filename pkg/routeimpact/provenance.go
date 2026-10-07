package routeimpact

import (
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// RepositoryReference returns a credential-free GitHub repository identity
// and an optional full commit in a github:// reference. Unsupported or
// malformed references return empty values; raw URLs must never be displayed.
// Other providers can be added when deployment metadata supports them.
func RepositoryReference(raw string) (string, string) {
	if raw == "" || len(raw) > api.RouteImpactIdentityMaxBytes || strings.TrimSpace(raw) != raw {
		return "", ""
	}
	var repository, revision string
	cloneReference := false
	switch {
	case strings.HasPrefix(strings.ToLower(raw), "git@github.com:"):
		repository = raw[len("git@github.com:"):]
		cloneReference = true
	case strings.HasPrefix(strings.ToLower(raw), "github.com/"):
		repository = raw[len("github.com/"):]
	default:
		u, err := url.Parse(raw)
		if err != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" {
			return "", ""
		}
		switch u.Scheme {
		case "https", "ssh":
			if !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" {
				return "", ""
			}
			repository = strings.TrimPrefix(u.Path, "/")
			cloneReference = true
		case "github":
			if u.User != nil || u.Port() != "" {
				return "", ""
			}
			repository = u.Host + "/" + strings.TrimPrefix(u.Path, "/")
			if name, commit, found := strings.Cut(repository, "@"); found {
				if !ValidCommit(commit) {
					return "", ""
				}
				repository, revision = name, strings.ToLower(commit)
			}
		default:
			return "", ""
		}
	}
	if cloneReference {
		repository = strings.TrimSuffix(repository, ".git")
	}
	owner, name, found := strings.Cut(repository, "/")
	if !found || !repositoryPart(owner) || !repositoryPart(name) {
		return "", ""
	}
	return "github.com/" + strings.ToLower(owner+"/"+name), revision
}

func repositoryPart(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if char != '.' && char != '_' && char != '-' && (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') {
			return false
		}
	}
	return true
}

// ValidCommit requires an unabbreviated SHA-1 or SHA-256 Git object ID.
func ValidCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
