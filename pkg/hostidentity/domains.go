// adr: 20261026100000
package hostidentity

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
)

// Domains is a deployment's public host layout (ADR-20261026100000).
//
//   - Platform is the domain whose apex, api. and operations. hosts are
//     platform-owned: the site, dashboard, API, emails and webhook URLs.
//   - Apps is the primary apps domain. App URLs Gregale hands out
//     (<slug>.<apps>, PR previews, deployment aliases) are built under it.
//   - Deploy is the domain deployment previews (deploy-<n>-<slug>) and
//     environment hosts (env-<id>-<id>) are built under. It defaults to
//     DefaultDeployDomain, which it always was.
//   - AppAliases are further apps domains that reach exactly the same apps,
//     so the URLs of a previous apps domain keep working after a move. With
//     AliasRedirect the public edge answers them with a permanent redirect
//     to the same name under Apps instead of serving tenant code there.
//
// Before ADR-20261026100000 one setting (FAAS_APPS_DOMAIN) played the
// platform and apps roles. Platform defaults to Apps, so a deployment that
// sets only FAAS_APPS_DOMAIN behaves exactly as before.
type Domains struct {
	Platform      string
	Apps          string
	Deploy        string
	AppAliases    []string
	AliasRedirect bool
}

// DefaultDeployDomain is the deployment-preview domain when none is set.
const DefaultDeployDomain = "gregale.dev"

// Environment variables naming the layout. FAAS_APPS_DOMAIN predates them.
const (
	EnvPlatformDomain       = "FAAS_PLATFORM_DOMAIN"
	EnvAppsDomain           = "FAAS_APPS_DOMAIN"
	EnvDeployDomain         = "FAAS_DEPLOY_DOMAIN"
	EnvAppsDomainAliases    = "FAAS_APPS_DOMAIN_ALIASES"
	EnvAppsDomainAliasMode  = "FAAS_APPS_DOMAIN_ALIAS_MODE"
	AliasModeServe          = "serve"
	AliasModeRedirect       = "redirect"
	maxAppDomainAliases     = 8
	envAliasModeErrorPrefix = "hostidentity: " + EnvAppsDomainAliasMode
)

// NewDomains normalizes and validates a layout. An empty platform domain
// takes the apps domain and an empty deploy domain DefaultDeployDomain. An
// empty apps domain (dev single-box, the e2e harness without one) means no
// host is an app host, and admits no aliases.
func NewDomains(platform, apps, deploy string, aliases []string, aliasMode string) (Domains, error) {
	d := Domains{Platform: normalizeDomain(platform), Apps: normalizeDomain(apps), Deploy: normalizeDomain(deploy)}
	if d.Platform == "" {
		d.Platform = d.Apps
	}
	if d.Deploy == "" {
		d.Deploy = DefaultDeployDomain
	}
	for _, alias := range aliases {
		if alias = normalizeDomain(alias); alias != "" && !slices.Contains(d.AppAliases, alias) {
			d.AppAliases = append(d.AppAliases, alias)
		}
	}
	switch strings.ToLower(strings.TrimSpace(aliasMode)) {
	case "", AliasModeServe:
	case AliasModeRedirect:
		d.AliasRedirect = true
	default:
		return d, fmt.Errorf("%s=%q: want %q or %q", envAliasModeErrorPrefix, aliasMode, AliasModeServe, AliasModeRedirect)
	}
	return d, d.validate()
}

func (d Domains) validate() error {
	if len(d.AppAliases) > maxAppDomainAliases {
		return fmt.Errorf("hostidentity: at most %d apps domain aliases", maxAppDomainAliases)
	}
	if d.Apps == "" && len(d.AppAliases) > 0 {
		return errors.New("hostidentity: apps domain aliases need an apps domain")
	}
	for _, name := range append([]string{d.Platform, d.Apps, d.Deploy}, d.AppAliases...) {
		if name != "" && !validDomain(name) {
			return fmt.Errorf("hostidentity: %q is not a domain name", name)
		}
	}
	apps := d.AppDomains()
	for i, a := range apps {
		for _, b := range apps[i+1:] {
			if a == b || strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a) {
				return fmt.Errorf("hostidentity: apps domains %q and %q overlap", a, b)
			}
		}
	}
	return nil
}

// AppDomains returns the primary apps domain followed by its aliases.
func (d Domains) AppDomains() []string {
	if d.Apps == "" {
		return nil
	}
	return append([]string{d.Apps}, d.AppAliases...)
}

// OwnedDomains returns every domain the deployment owns, without duplicates:
// no name under one of them is a customer's custom domain.
func (d Domains) OwnedDomains() []string {
	var out []string
	for _, name := range append([]string{d.Platform, d.Deploy}, d.AppDomains()...) {
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// IsPlatformHost reports whether host (lower-case, no port) is a
// platform-owned host: the platform apex or its api. or operations. host.
// With no platform domain configured every host is a platform host, as
// before.
func (d Domains) IsPlatformHost(host string) bool {
	if d.Platform == "" {
		return true
	}
	return host == d.Platform || host == "api."+d.Platform || host == "operations."+d.Platform
}

// IsAppHost reports whether host is one label under an apps domain and not
// a platform host.
func (d Domains) IsAppHost(host string) bool {
	_, ok := d.CanonicalAppHost(host)
	return ok
}

// CanonicalAppHost maps an app host under any apps domain to the same label
// under the primary apps domain. The public edge rewrites an alias request to
// the canonical host, so it reaches exactly the app, preview or alias its
// label names under the primary domain, under the same host-keyed policy.
// Hosts that are not one label under an apps domain, and platform hosts,
// report false.
func (d Domains) CanonicalAppHost(host string) (string, bool) {
	if d.Apps == "" || d.IsPlatformHost(host) {
		return "", false
	}
	for _, domain := range d.AppDomains() {
		label, ok := strings.CutSuffix(host, "."+domain)
		if ok && label != "" && !strings.Contains(label, ".") {
			return label + "." + d.Apps, true
		}
	}
	return "", false
}

// AliasHosts returns host's name under each alias apps domain when host is
// one label under the primary apps domain: the names a customer may have
// stored for it (an edge rule's match_host) before the apps domain moved.
func (d Domains) AliasHosts(host string) []string {
	label, ok := strings.CutSuffix(host, "."+d.Apps)
	if len(d.AppAliases) == 0 || !ok || label == "" || strings.Contains(label, ".") {
		return nil
	}
	out := make([]string, 0, len(d.AppAliases))
	for _, alias := range d.AppAliases {
		if name := label + "." + alias; !d.IsPlatformHost(name) {
			out = append(out, name)
		}
	}
	return out
}

// CanonicalHostPattern maps a host or one-label host glob ("*.<alias>")
// under an alias apps domain to the same pattern under the primary apps
// domain. Other patterns are returned normalized and otherwise unchanged.
func (d Domains) CanonicalHostPattern(pattern string) string {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if d.Apps == "" || d.IsPlatformHost(pattern) {
		return pattern
	}
	for _, alias := range d.AppAliases {
		label, ok := strings.CutSuffix(pattern, "."+alias)
		if ok && label != "" && !strings.Contains(label, ".") {
			return label + "." + d.Apps
		}
	}
	return pattern
}

// Owns reports whether host is an owned domain or a name under one.
func (d Domains) Owns(host string) bool {
	for _, domain := range d.OwnedDomains() {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func normalizeDomain(name string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(name)), ".")
}

func validDomain(name string) bool {
	if len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// SplitAliases parses a comma- or space-separated alias list.
func SplitAliases(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
}

// FileConfig is the part of a daemon's TOML that names the layout.
type FileConfig struct {
	PlatformDomain      string   `toml:"platform_domain"`
	DeployDomain        string   `toml:"deploy_domain"`
	AppsDomainAliases   []string `toml:"apps_domain_aliases"`
	AppsDomainAliasMode string   `toml:"apps_domain_alias_mode"`
}

// DomainsFromEnv resolves the layout: each environment variable wins over
// the daemon's configuration file, as FAAS_APPS_DOMAIN always has. apps is
// the apps domain the daemon resolved from its own configuration.
func DomainsFromEnv(getenv func(string) string, apps string, file FileConfig) (Domains, error) {
	pick := func(env, fallback string) string {
		if v := strings.TrimSpace(getenv(env)); v != "" {
			return v
		}
		return fallback
	}
	if v := strings.TrimSpace(getenv(EnvAppsDomain)); v != "" {
		apps = v
	}
	aliases := file.AppsDomainAliases
	if v := strings.TrimSpace(getenv(EnvAppsDomainAliases)); v != "" {
		aliases = SplitAliases(v)
	}
	return NewDomains(pick(EnvPlatformDomain, file.PlatformDomain), apps,
		pick(EnvDeployDomain, file.DeployDomain), aliases, pick(EnvAppsDomainAliasMode, file.AppsDomainAliasMode))
}

// The process-wide layout. Daemons set it once while wiring, before
// serving; code with no configuration plumbing of its own (the deployment
// preview and environment host builders and parsers) reads it. Until a
// daemon configures it, every read derives it from the environment, with
// DefaultAppsDomain as the apps domain.
var (
	configuredMu sync.RWMutex
	configured   *Domains
)

// Configure installs the process-wide layout and returns a function that
// restores the previous one (for tests).
func Configure(d Domains) (restore func()) {
	c := d
	c.AppAliases = slices.Clone(d.AppAliases)
	configuredMu.Lock()
	previous := configured
	configured = &c
	configuredMu.Unlock()
	return func() {
		configuredMu.Lock()
		configured = previous
		configuredMu.Unlock()
	}
}

// Configured returns the process-wide layout.
func Configured() Domains {
	configuredMu.RLock()
	c := configured
	configuredMu.RUnlock()
	if c != nil {
		return *c
	}
	d, err := DomainsFromEnv(os.Getenv, DefaultAppsDomain, FileConfig{})
	if err != nil {
		d, _ = NewDomains("", DefaultAppsDomain, "", nil, "")
	}
	return d
}

// DeploySuffix is the leading-dot suffix deployment previews and
// environment hosts are built and parsed under.
func DeploySuffix() string {
	configuredMu.RLock()
	c := configured
	configuredMu.RUnlock()
	if c != nil {
		return AppsSuffix(c.Deploy)
	}
	if deploy := normalizeDomain(os.Getenv(EnvDeployDomain)); validDomain(deploy) {
		return AppsSuffix(deploy)
	}
	return AppsSuffix(DefaultDeployDomain)
}
