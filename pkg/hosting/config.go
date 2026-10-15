package hosting

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

type Config struct {
	Domain    string
	Artifacts *R2
	Routes    *KV
}

// Load performs no network writes and provisions no resources. Operators must
// supply an isolated hosting domain, private bucket, KV, and Access policy.
func Load(getenv func(string) string, appsDomain string) (*Config, error) {
	domain := strings.ToLower(strings.TrimSpace(getenv("FAAS_FRONTEND_HOSTING_DOMAIN")))
	if domain == "" {
		if getenv("FAAS_FRONTEND_HOSTING_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_ENABLED") == "true" {
			return nil, errors.New("enabled hosting requires an isolated hosting domain and provider configuration")
		}
		return nil, nil
	}
	u, err := url.Parse("https://" + domain)
	root, rootErr := publicsuffix.EffectiveTLDPlusOne(domain)
	appRoot, appErr := publicsuffix.EffectiveTLDPlusOne(appsDomain)
	if err != nil || rootErr != nil || u.Hostname() != domain || u.Path != "" || u.Port() != "" || appErr == nil && root == appRoot {
		return nil, errors.New("hosting requires a valid domain on a different registrable domain from platform apps")
	}
	b, err := NewR2(getenv("FAAS_FRONTEND_HOSTING_R2_ENDPOINT"), getenv("FAAS_FRONTEND_HOSTING_R2_BUCKET"), getenv("FAAS_FRONTEND_HOSTING_R2_ACCESS_KEY"), getenv("FAAS_FRONTEND_HOSTING_R2_SECRET_KEY"))
	if err != nil {
		return nil, err
	}
	p, err := NewKV(getenv("FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID"), getenv("FAAS_FRONTEND_HOSTING_KV_NAMESPACE_ID"), getenv("FAAS_FRONTEND_HOSTING_CF_TOKEN"))
	if err != nil {
		return nil, err
	}
	return &Config{Domain: domain, Artifacts: b, Routes: p}, nil
}
