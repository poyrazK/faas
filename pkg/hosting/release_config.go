package hosting

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const ReleaseConfigFilename = "gregale-hosting.json"

// ParseReleaseConfig consumes a bounded, versioned configuration file. Unknown
// fields are errors so a misspelled policy cannot silently deploy.
func ParseReleaseConfig(data []byte) (*api.HostingReleaseConfig, error) {
	if len(data) > api.HostingConfigMaxBytes {
		return nil, errors.New("hosting configuration exceeds its byte limit")
	}
	var cfg api.HostingReleaseConfig
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return nil, errors.New("invalid hosting configuration JSON")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("hosting configuration requires one JSON object")
	}
	if err := ValidateReleaseConfig(&cfg, nil); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func configPath(p string) bool {
	if p == "/" {
		return true
	}
	return strings.HasPrefix(p, "/") && len(p) <= api.HostingPathMaxBytes &&
		strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/") != "_gregale" &&
		ValidPath(strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/"))
}

func ValidConfigHeader(name, value string) bool {
	switch name {
	case "content-security-policy", "content-security-policy-report-only", "permissions-policy", "referrer-policy", "x-frame-options", "cross-origin-opener-policy", "cross-origin-embedder-policy", "cross-origin-resource-policy":
	default:
		return false
	}
	if len(value) == 0 || len(value) > api.HostingConfigHeaderValueMaxBytes || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

// A nil file list validates syntax only. With files present, custom 404 content
// must exist as HTML in this exact release, never a retained release.
func ValidateReleaseConfig(cfg *api.HostingReleaseConfig, files []api.HostingFile) error {
	if cfg == nil {
		return nil
	}
	invalid := errors.New("invalid hosting configuration: check version, paths, redirects, approved headers, and 404 file")
	data, err := json.Marshal(cfg)
	if err != nil || len(data) > api.HostingConfigMaxBytes || cfg.Version != 1 || len(cfg.Redirects) > api.HostingConfigRedirectsMax || len(cfg.Headers) > api.HostingConfigHeaderRulesMax {
		return invalid
	}
	redirects := make(map[string]string, len(cfg.Redirects))
	for _, r := range cfg.Redirects {
		if !configPath(r.From) || !configPath(r.To) || redirects[r.From] != "" || (r.Status != 301 && r.Status != 302 && r.Status != 307 && r.Status != 308) {
			return invalid
		}
		redirects[r.From] = r.To
	}
	for source := range redirects {
		seen := map[string]bool{}
		for p := source; redirects[p] != ""; p = redirects[p] {
			if seen[p] {
				return invalid
			}
			seen[p] = true
		}
	}
	seen := map[string]bool{}
	for _, rule := range cfg.Headers {
		p := strings.TrimSuffix(rule.Path, "*")
		if !configPath(p) || strings.Contains(rule.Path, "*") && !strings.HasSuffix(rule.Path, "/*") || seen[rule.Path] || len(rule.Values) == 0 || len(rule.Values) > api.HostingConfigHeadersPerRuleMax {
			return invalid
		}
		seen[rule.Path] = true
		for name, value := range rule.Values {
			if !ValidConfigHeader(name, value) {
				return invalid
			}
		}
	}
	if cfg.NotFound != "" {
		if !ValidPath(cfg.NotFound) {
			return invalid
		}
		if files != nil {
			found := false
			for _, f := range files {
				if f.Path == cfg.NotFound && strings.EqualFold(strings.Split(f.ContentType, ";")[0], "text/html") {
					found = true
				}
			}
			if !found {
				return invalid
			}
		}
	}
	return nil
}
