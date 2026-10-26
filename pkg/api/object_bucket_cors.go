package api

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

// ObjectBucketCORSRule is one S3 CORSRule a customer sets on a bucket
// (ADR-20261025202805). The branded gateway applies the first rule that
// matches a request's origin, method and requested headers.
type ObjectBucketCORSRule struct {
	ID             string   `json:"id,omitempty"`
	AllowedOrigins []string `json:"allowed_origins"`
	AllowedMethods []string `json:"allowed_methods"`
	AllowedHeaders []string `json:"allowed_headers,omitempty"`
	ExposeHeaders  []string `json:"expose_headers,omitempty"`
	MaxAgeSeconds  int      `json:"max_age_seconds,omitempty"`
}

type ObjectBucketCORSRequest struct {
	Rules []ObjectBucketCORSRule `json:"rules"`
}

type ObjectBucketCORS struct {
	BucketID  string                 `json:"bucket_id"`
	Rules     []ObjectBucketCORSRule `json:"rules"`
	Revision  int64                  `json:"revision"`
	UpdatedAt time.Time              `json:"updated_at"`
}

// objectBucketCORSMethods are the methods a rule may allow, as in S3.
var objectBucketCORSMethods = map[string]bool{"GET": true, "PUT": true, "POST": true, "DELETE": true, "HEAD": true}

// ValidObjectBucketCORSRules reports whether rules form a bucket CORS
// configuration within MaxObjectBucketCORSRules and the per-rule bounds.
func ValidObjectBucketCORSRules(rules []ObjectBucketCORSRule) bool {
	if len(rules) < 1 || len(rules) > MaxObjectBucketCORSRules {
		return false
	}
	ids := map[string]bool{}
	for _, rule := range rules {
		if len(rule.ID) > MaxObjectBucketCORSRuleIDBytes || rule.ID != "" && ids[rule.ID] {
			return false
		}
		if rule.ID != "" {
			ids[rule.ID] = true
		}
		if rule.MaxAgeSeconds < 0 || rule.MaxAgeSeconds > MaxObjectBucketCORSMaxAgeSeconds {
			return false
		}
		if !validCORSList(rule.AllowedOrigins, 1, validCORSOrigin) || !validCORSList(rule.AllowedMethods, 1, func(m string) bool { return objectBucketCORSMethods[m] }) ||
			!validCORSList(rule.AllowedHeaders, 0, validCORSHeaderPattern) || !validCORSList(rule.ExposeHeaders, 0, validCORSHeaderName) {
			return false
		}
	}
	return true
}

func validCORSList(values []string, minimum int, valid func(string) bool) bool {
	if len(values) < minimum || len(values) > MaxObjectBucketCORSRuleValues {
		return false
	}
	for _, v := range values {
		if v == "" || len(v) > MaxObjectBucketCORSValueBytes || !valid(v) {
			return false
		}
	}
	return true
}

// validCORSOrigin accepts "*" or an http(s) origin with at most one "*".
func validCORSOrigin(origin string) bool {
	if origin == "*" {
		return true
	}
	if strings.Count(origin, "*") > 1 || strings.ContainsAny(origin, " \t\r\n") {
		return false
	}
	u, err := url.Parse(strings.Replace(origin, "*", "wildcard", 1))
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && u.User == nil
}

func validCORSHeaderPattern(name string) bool {
	return strings.Count(name, "*") <= 1 && validCORSHeaderName(strings.Replace(name, "*", "x", 1))
}

func validCORSHeaderName(name string) bool {
	for _, c := range name {
		letter := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (c < '0' || c > '9') && !strings.ContainsRune("-_.!#$%&'+^`|~", c) {
			return false
		}
	}
	return name != ""
}

// MatchObjectBucketCORS returns the first rule allowing origin, method and
// every requested header (lowercased, comma-separated as a browser sends them).
func MatchObjectBucketCORS(rules []ObjectBucketCORSRule, origin, method string, requested []string) (ObjectBucketCORSRule, bool) {
	for _, rule := range rules {
		if !anyCORSMatch(rule.AllowedOrigins, origin, false) || !slices.Contains(rule.AllowedMethods, method) {
			continue
		}
		allowed := true
		for _, header := range requested {
			if !anyCORSMatch(rule.AllowedHeaders, header, true) {
				allowed = false
				break
			}
		}
		if allowed {
			return rule, true
		}
	}
	return ObjectBucketCORSRule{}, false
}

func anyCORSMatch(patterns []string, value string, fold bool) bool {
	for _, pattern := range patterns {
		if fold {
			pattern, value = strings.ToLower(pattern), strings.ToLower(value)
		}
		before, after, wildcard := strings.Cut(pattern, "*")
		if !wildcard && pattern == value || wildcard && len(value) >= len(before)+len(after) && strings.HasPrefix(value, before) && strings.HasSuffix(value, after) {
			return true
		}
	}
	return false
}
