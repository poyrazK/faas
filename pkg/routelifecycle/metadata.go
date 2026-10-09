// Package routelifecycle validates advisory OpenAPI operation lifecycle metadata.
package routelifecycle

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Metadata is persisted in the imported operation, without a separate policy store.
type Metadata struct {
	DeprecatedAt time.Time
	SunsetAt     time.Time
	Successor    string
}

// Parse accepts RFC3339 dates and an absolute HTTPS successor documentation URL.
func Parse(operation map[string]any) (Metadata, error) {
	var m Metadata
	for _, field := range []struct {
		name   string
		target *time.Time
	}{
		{"x-gregale-deprecated-at", &m.DeprecatedAt}, {"x-gregale-sunset-at", &m.SunsetAt},
	} {
		value, present := operation[field.name]
		if !present {
			continue
		}
		text, ok := value.(string)
		if !ok {
			return m, fmt.Errorf("%s must be an RFC3339 string", field.name)
		}
		date, err := time.Parse(time.RFC3339, text)
		if err != nil || date.Unix() < 0 || date.Nanosecond() != 0 {
			return m, fmt.Errorf("%s must be a whole-second RFC3339 date after the Unix epoch", field.name)
		}
		*field.target = date.UTC()
	}
	if value, present := operation["x-gregale-successor"]; present {
		text, ok := value.(string)
		u, err := url.Parse(text)
		if !ok || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.ContainsAny(text, "<>\"\r\n\\ ") {
			return m, fmt.Errorf("x-gregale-successor must be an absolute HTTPS URL without credentials")
		}
		m.Successor = text
	}
	if !m.DeprecatedAt.IsZero() || !m.SunsetAt.IsZero() || m.Successor != "" {
		if operation["deprecated"] != true {
			return m, fmt.Errorf("lifecycle metadata requires deprecated: true")
		}
		if m.DeprecatedAt.IsZero() {
			return m, fmt.Errorf("lifecycle metadata requires x-gregale-deprecated-at")
		}
	}
	if !m.SunsetAt.IsZero() {
		if m.SunsetAt.Before(m.DeprecatedAt) {
			return m, fmt.Errorf("sunset must not precede deprecation")
		}
		if m.Successor == "" {
			return m, fmt.Errorf("sunset requires x-gregale-successor")
		}
	}
	return m, nil
}

// Apply publishes RFC 9745 and RFC 8594 dates; successor-version is an RFC 5829 link.
func (m Metadata) Apply(header http.Header) {
	if !m.DeprecatedAt.IsZero() {
		header.Set("Deprecation", "@"+strconv.FormatInt(m.DeprecatedAt.Unix(), 10))
	}
	if !m.SunsetAt.IsZero() {
		header.Set("Sunset", m.SunsetAt.Format(http.TimeFormat))
	}
	if m.Successor != "" {
		header.Add("Link", "<"+m.Successor+">; rel=\"successor-version\"")
	}
}
