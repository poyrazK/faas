package gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// isBrowserDocumentNavigation distinguishes a page load from an API fetch so
// the release cookie is not rotated by ordinary requests from a long-lived
// client after a graph cutover.
func isBrowserDocumentNavigation(r *http.Request) bool {
	if r == nil || r.Method != http.MethodGet {
		return false
	}
	if dest := strings.TrimSpace(r.Header.Get("Sec-Fetch-Dest")); dest != "" {
		return strings.EqualFold(dest, "document")
	}
	for _, value := range r.Header.Values("Accept") {
		for _, mediaType := range strings.Split(value, ",") {
			mediaType, _, _ = strings.Cut(strings.TrimSpace(mediaType), ";")
			if strings.EqualFold(strings.TrimSpace(mediaType), "text/html") ||
				strings.EqualFold(strings.TrimSpace(mediaType), "application/xhtml+xml") {
				return true
			}
		}
	}
	return false
}

func setManagedReleaseContextCookie(w http.ResponseWriter, releaseID string) string {
	cookie := &http.Cookie{
		Name:     api.ManagedReleaseContextCookieName,
		Value:    releaseID,
		Path:     "/",
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
	return cookie.String()
}

func clearManagedReleaseContextCookie(w http.ResponseWriter) string {
	cookie := &http.Cookie{
		Name:     api.ManagedReleaseContextCookieName,
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(w, cookie)
	return cookie.String()
}

// managedReleaseContextCookieValue reads the platform bootstrap cookie before
// it is stripped from the guest request. Duplicate names are ambiguous and
// must not silently select whichever value happens to be parsed first.
func managedReleaseContextCookieValue(r *http.Request) (value string, present, duplicate bool) {
	if r == nil {
		return "", false, false
	}
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			name, raw, hasValue := strings.Cut(strings.TrimSpace(part), "=")
			if strings.TrimSpace(name) != api.ManagedReleaseContextCookieName {
				continue
			}
			if present {
				return "", true, true
			}
			present = true
			if hasValue {
				value = strings.TrimSpace(raw)
			}
		}
	}
	return value, present, false
}

// stripManagedReleaseContextCookie keeps the platform bootstrap value out of
// guest requests while leaving application cookies untouched.
func stripManagedReleaseContextCookie(r *http.Request) {
	if r == nil {
		return
	}
	var kept []string
	for _, line := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, _, _ := strings.Cut(part, "=")
			if strings.TrimSpace(name) != api.ManagedReleaseContextCookieName {
				kept = append(kept, part)
			}
		}
	}
	r.Header.Del("Cookie")
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}
