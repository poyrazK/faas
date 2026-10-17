package hosting

import (
	"errors"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

type Config struct {
	Domain          string
	Artifacts       *R2
	Routes          *KV
	Domains         *CloudflareDomains
	Analytics       *Analytics
	ReservedDomains []string
}

// Load performs no network writes and provisions no resources. Operators must
// supply an isolated hosting domain, private bucket, KV, and Access policy.
func Load(getenv func(string) string, appsDomain string) (*Config, error) {
	domain := strings.ToLower(strings.TrimSpace(getenv("FAAS_FRONTEND_HOSTING_DOMAIN")))
	if domain == "" {
		if getenv("FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_CANCELLATION_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_PREVIEW_RESERVATIONS_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_ACTIVITY_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_CI_SETUP_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_DIAGNOSTICS_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_OIDC_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_OIDC_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_RETENTION_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_RETENTION_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_AUTOMATION_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_HISTORY_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_PREVIEWS_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_CONFIG_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_PILOT_ENABLED") == "1" || getenv("FAAS_FRONTEND_HOSTING_PILOT_ENABLED") == "true" || getenv("FAAS_FRONTEND_HOSTING_PILOT_RELAY_SOCKET") != "" {
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
	cfg := &Config{Domain: domain, Artifacts: b, Routes: p, ReservedDomains: []string{domain, appsDomain}}
	for _, reserved := range strings.Split(getenv("FAAS_FRONTEND_HOSTING_RESERVED_DOMAINS"), ",") {
		if reserved = strings.TrimSpace(reserved); reserved != "" {
			if _, err := publicsuffix.EffectiveTLDPlusOne(reserved); err != nil {
				return nil, errors.New("invalid reserved hosting domain")
			}
			cfg.ReservedDomains = append(cfg.ReservedDomains, reserved)
		}
	}
	if getenv("FAAS_FRONTEND_HOSTING_DOMAINS_ZONE_ID") != "" {
		cfg.Domains, err = NewCloudflareDomains(getenv("FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID"), getenv("FAAS_FRONTEND_HOSTING_DOMAINS_ZONE_ID"), getenv("FAAS_FRONTEND_HOSTING_DOMAINS_ACCESS_POLICY_ID"), getenv("FAAS_FRONTEND_HOSTING_DOMAINS_CF_TOKEN"), getenv("FAAS_FRONTEND_HOSTING_DOMAINS_CNAME_TARGET"))
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(cfg.Domains.Target(), "."+domain) {
			return nil, errors.New("domain CNAME target must belong to the isolated hosting zone")
		}
	} else if value := getenv("FAAS_FRONTEND_HOSTING_DOMAINS_ENABLED"); value == "1" || value == "true" {
		return nil, errors.New("hosting custom domains require provider configuration")
	}
	if dataset := getenv("FAAS_FRONTEND_HOSTING_ANALYTICS_DATASET"); dataset != "" {
		cfg.Analytics, err = NewAnalytics(getenv("FAAS_FRONTEND_HOSTING_CF_ACCOUNT_ID"), dataset, getenv("FAAS_FRONTEND_HOSTING_ANALYTICS_CF_TOKEN"))
		if err != nil {
			return nil, err
		}
	} else if value := getenv("FAAS_FRONTEND_HOSTING_ANALYTICS_ENABLED"); value == "1" || value == "true" {
		return nil, errors.New("hosting analytics requires provider configuration")
	}
	if err := configurePilot(cfg, getenv); err != nil {
		return nil, err
	}
	return cfg, nil
}
