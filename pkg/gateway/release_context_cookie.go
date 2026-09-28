package gateway

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
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

// consumeManagedReleaseSubprotocol extracts the reserved browser release
// carrier from a WebSocket handshake and removes it before guest forwarding.
// Application protocols retain their original order. The prefix is reserved;
// malformed or duplicate values fail closed rather than becoming app protocols.
func consumeManagedReleaseSubprotocol(r *http.Request) (releaseID string, present, invalid bool) {
	if r == nil {
		return "", false, false
	}
	var appProtocols []string
	for _, value := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, item := range strings.Split(value, ",") {
			protocol := strings.TrimSpace(item)
			if !strings.HasPrefix(protocol, api.ManagedReleaseSubprotocolPrefix) {
				if protocol != "" {
					appProtocols = append(appProtocols, protocol)
				}
				continue
			}
			if present {
				invalid = true
				continue
			}
			present = true
			value := strings.TrimPrefix(protocol, api.ManagedReleaseSubprotocolPrefix)
			parsed, err := uuid.Parse(value)
			if len(value) != 36 || err != nil {
				invalid = true
				continue
			}
			releaseID = parsed.String()
		}
	}
	if !present {
		return "", false, false
	}
	r.Header.Del("Sec-WebSocket-Protocol")
	if len(appProtocols) > 0 {
		r.Header.Set("Sec-WebSocket-Protocol", strings.Join(appProtocols, ", "))
	}
	return releaseID, true, invalid
}

// stripManagedReleaseSubprotocol removes a guest attempt to negotiate the
// platform's reserved carrier back to the browser. Application subprotocols
// in the same header remain visible.
func stripManagedReleaseSubprotocol(value string) (string, bool) {
	var protocols []string
	for _, item := range strings.Split(value, ",") {
		protocol := strings.TrimSpace(item)
		if protocol == "" || strings.HasPrefix(protocol, api.ManagedReleaseSubprotocolPrefix) {
			continue
		}
		protocols = append(protocols, protocol)
	}
	if len(protocols) == 0 {
		return "", false
	}
	return strings.Join(protocols, ", "), true
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
