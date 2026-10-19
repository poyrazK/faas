package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"strings"
)

// ProjectPublicBuildURLs contains explicitly reviewed public URL values only.
// Unlike initial_secrets, these values are saved and may appear in bundles.
type ProjectPublicBuildURLs map[string]map[string]string

func ValidatePublicBuildURLKey(key string) *Problem {
	if ValidateSecretKey(key) != nil || !strings.HasSuffix(key, "_URL") {
		return ErrValidation("public build URL keys must use a frontend prefix and end in _URL")
	}
	for _, prefix := range []string{"VITE_", "NEXT_PUBLIC_", "REACT_APP_", "PUBLIC_", "NUXT_PUBLIC_", "GATSBY_"} {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix)+len("_URL") {
			return nil
		}
	}
	return ErrValidation("public build URL keys must use a frontend prefix and end in _URL")
}

// ValidatePublicBuildURL deliberately accepts only public HTTPS base URLs.
// Credentials, query strings and fragments must never enter this public path.
// This validates configuration; it performs no network requests.
func ValidatePublicBuildURL(value string) *Problem {
	u, err := url.Parse(value)
	if err != nil || len(value) > PublicBuildURLMaxBytes || strings.TrimSpace(value) != value ||
		u == nil || u.Scheme != "https" || u.Opaque != "" || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(value, "#") || strings.ContainsAny(value, "\r\n\x00") {
		return ErrValidation("use a public HTTPS base URL without credentials, query parameters or fragments")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || !strings.Contains(host, ".") ||
		net.ParseIP(host) != nil || host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".test") ||
		(u.Port() != "" && u.Port() != "443") {
		return ErrValidation("use a public HTTPS hostname for frontend build URLs")
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return ErrValidation("use a public HTTPS hostname for frontend build URLs")
		}
		for _, c := range label {
			if c != '-' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
				return ErrValidation("use a public HTTPS hostname for frontend build URLs")
			}
		}
	}
	return nil
}

func ValidatePublicBuildURLs(values map[string]string) *Problem {
	if len(values) > PublicBuildURLsMaxPerApp {
		return ErrValidation("at most ten public build URLs may be configured per service")
	}
	for key, value := range values {
		if problem := ValidatePublicBuildURLKey(key); problem != nil {
			return problem
		}
		if problem := ValidatePublicBuildURL(value); problem != nil {
			return problem
		}
	}
	raw, _ := json.Marshal(values)
	// jsonb's text representation adds at most two spaces per entry.
	if len(raw)+2*len(values) > PublicBuildURLsEncodedMaxBytes {
		return ErrValidation("public build URL configuration exceeds its encoded size limit")
	}
	return nil
}

// PublicBuildURLsSHA256 binds guest acknowledgement and artifact caching to
// the same deterministic values. Empty configuration preserves legacy builds.
func PublicBuildURLsSHA256(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
