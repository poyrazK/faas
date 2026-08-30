// Package main's config — parsed from /etc/faas/apid.toml (or the path
// passed via --config). Each field is independent of every other so a
// partial config file plus defaults produces a working daemon.
//
// This is the issue-#678 extraction surface (PR-0): every TOML field
// listed here is sourced from the same file that billingloader.LoadBillingConfigFromPath
// already reads for the [billing] block — so two readers see one source
// of truth. Behaviour-preserving: every inline env read in
// cmd/apid/main.go that used to read FAAS_APID_* or FAAS_GITHUBD_*
// (etc.) now goes through one of the helpers below; the env vars
// continue to win over the TOML value because the helpers are called
// after LoadConfig in main.go and the env-overlay pattern is preserved.
//
// PR-0 only adds the type, LoadConfig, the Get helpers, and the Load*
// TLS helpers (mirroring cmd/vmmd/config.go's shape). PR-B adds the
// *WithVerifier variants AND wires the verifier construction —
// collapsed into one PR per the post-PR-823 sequencing decision
// ("one big PR-B" instead of stacked PR-A + PR-B).
package main

import (
	"crypto/tls"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Config is the on-disk representation of apid's TOML config.
// File reads use BurntSushi/toml (already a transitive dep of
// many tools; pinning it here makes the daemon's config story
// explicit).
type Config struct {
	// ListenAddr is the loopback bind address for the customer-facing
	// REST API + dashboard. Defaults to 127.0.0.1:8081 (legacy single-
	// box default; gatewayd-public reverse-proxies 0.0.0.0:443 in
	// front). Mirrors the legacy FAAS_APID_LISTEN env var.
	ListenAddr string `toml:"listen_addr"`

	// MetricsAddr is the optional bind address for /metrics. Empty
	// disables the listener. Mirrors FAAS_APID_METRICS_ADDR.
	MetricsAddr string `toml:"metrics_addr"`

	// AdvisorySock is the unix-domain socket the stateless-advisory
	// gRPC server binds when set (vmmd dials /run/faas/apid.sock to
	// forward fanotify batches from guest-init). Empty disables.
	// Mirrors FAAS_APID_ADVISORY_SOCK.
	//
	// PR-0 (issue #678): default stays empty (the pre-PR-0 env-only
	// shape). Setting a non-empty default here would auto-enable the
	// listener on every e2e test that doesn't set the env var, and
	// the bind fails in CI because the systemd-owned `faas-apid`
	// user isn't present in the test container. Production sets
	// the env (or the TOML key) to opt in.
	AdvisorySock string `toml:"advisory_sock"`

	// GithubdBridgeSock is the unix-domain socket the githubd → apid
	// build-enqueue bridge gRPC server binds when set. Empty disables.
	// Mirrors FAAS_APID_GITHUBD_BRIDGE_SOCK.
	//
	// PR-0 (issue #678): default stays empty for the same reason as
	// AdvisorySock (CI users `faas-githubd` doesn't exist either).
	GithubdBridgeSock string `toml:"githubd_bridge_sock"`

	// AppErrorsTarget is the gatewayd-internal → apid AppErrors gRPC
	// endpoint. The default is the legacy local Unix socket; split-box
	// manifests set a tcp:// target and the TLS paths below.
	AppErrorsTarget string `toml:"app_errors_target"`

	// AppErrorsTLS is the server mTLS material for the AppErrors listener.
	// Empty paths preserve the single-box Unix-socket path.
	AppErrorsTLSCertPath string `toml:"app_errors_tls_cert_path"`
	AppErrorsTLSKeyPath  string `toml:"app_errors_tls_key_path"`
	AppErrorsTLSCAPath   string `toml:"app_errors_tls_ca_path"`

	// GithubdSocket is the unix-domain socket apid dials to call
	// githubd's EnqueueBuild RPC (issue #98 / ADR-028 phase). Empty
	// uses the newGithubdClient stub-client path (every method
	// returns api.Problem{Code:"githubd_not_ready"}). Mirrors
	// FAAS_GITHUBD_SOCKET. Defaults to /run/faas/githubd.sock.
	GithubdSocket string `toml:"githubd_socket"`

	// AppsDomain is the platform wildcard suffix (e.g. "gregale.dev").
	// apid renders the wildcard-aware /login template that lets the
	// dashboard build <slug>.<domain> links per app. Empty disables
	// the wildcard UI (custom-domain-only deployments). Mirrors
	// FAAS_APPS_DOMAIN. The public Gregale default is gregale.dev.
	AppsDomain string `toml:"apps_domain"`

	// CLIAuthURLBase is the absolute API origin that serves the browser
	// half of the CLI device-code flow. It is intentionally separate from
	// AppsDomain: app URLs may use the public wildcard domain while the
	// /cli-auth route is served by the control-plane API host. Mirrors
	// FAAS_CLI_AUTH_URL_BASE. Bare hostnames are normalized to HTTPS.
	CLIAuthURLBase string `toml:"cli_auth_url_base"`

	// DBURL is apid's Postgres DSN. An empty value preserves the
	// containerised-deploys path and lets db.Open resolve DATABASE_URL or
	// its local Unix-socket default. Manifest-rendered control-plane TOML
	// carries the local socket DSN so an old TCP environment value cannot
	// silently override the topology.
	DBURL string `toml:"db_url"`

	// Server-mTLS material for the advisory listener (ADR-052 /
	// issue #95). All three paths empty => no TLS, single-box unix
	// socket path; all three set => RequireAndVerifyClientCert for
	// the multi-box tcp/dns path. Partial cluster => startup error
	// naming the missing fields. Mirrors
	// FAAS_APID_ADVISORY_TLS_{CERT,KEY,CA}_PATH env trio.
	AdvisoryTLSCertPath string `toml:"advisory_tls_cert_path"`
	AdvisoryTLSKeyPath  string `toml:"advisory_tls_key_path"`
	AdvisoryTLSCAPath   string `toml:"advisory_tls_ca_path"`

	// GithubdBridgeServerTLS is the server-mTLS material for the
	// githubd → apid bridge listener (ADR-052). Same partial-cluster
	// contract as AdvisoryTLS*. Mirrors
	// FAAS_APID_GITHUBD_BRIDGE_TLS_{CERT,KEY,CA}_PATH env trio.
	GithubdBridgeTLSCertPath string `toml:"githubd_bridge_tls_cert_path"`
	GithubdBridgeTLSKeyPath  string `toml:"githubd_bridge_tls_key_path"`
	GithubdBridgeTLSCAPath   string `toml:"githubd_bridge_tls_ca_path"`

	// GithubdClientTLS is the client-mTLS material apid uses to
	// dial githubd's EnqueueBuild gRPC server (ADR-052). Same
	// partial-cluster contract as AdvisoryTLS*. Mirrors
	// FAAS_GITHUBD_TLS_{CERT,KEY,CA}_PATH env trio.
	GithubdClientTLSCertPath string `toml:"githubd_tls_cert_path"`
	GithubdClientTLSKeyPath  string `toml:"githubd_tls_key_path"`
	GithubdClientTLSCAPath   string `toml:"githubd_tls_ca_path"`

	// NodeName is the multi-box identity for the apid process
	// (issue #678 / ADR-093 PR-0). When non-empty, apid is in
	// multi-box mode: PR-B constructs PGNodeVerifier and threads
	// it through every Load*WithVerifier helper. When empty,
	// the verifier stays nil and stdlib trust alone runs (the
	// single-box dev back-compat path). Operator seeds the
	// matching row in compute_nodes via the existing
	// POST /v1/compute-nodes flow (no new apid handler —
	// reuses UpsertComputeNodeFromOperator). Defaults to "".
	NodeName string `toml:"node_name"`

	// Role is the box shape this apid inhabits (Gate-B; env
	// override FAAS_APID_ROLE wins when set). apid is a
	// control-plane daemon — it refuses to start under
	// RoleComputeOnly. RoleSingleBox is the default and lets
	// single-box dev boot unmoved.
	Role role.Role `toml:"role"`

	// Request timeouts (issue #995 Phase 1 / ADR-121). Default 0
	// means "use api.APID*SecondsDefault" — see GetRequestReadTimeout
	// etc. The env overlay pattern matches GetListenAddr / GetMetricsAddr.
	// Plain integer nanoseconds are NOT accepted (BurntSushi/toml Go
	// time.Duration string syntax — "60s", "5m", "1h30m").
	RequestReadTimeout    time.Duration `toml:"request_read_timeout"`
	RequestWriteTimeout   time.Duration `toml:"request_write_timeout"`
	RequestIdleTimeout    time.Duration `toml:"request_idle_timeout"`
	RequestMaxHeaderBytes int64         `toml:"request_max_header_bytes"`
}

// LoadConfig reads a TOML file at path and returns the parsed Config
// with defaults filled in. A missing file is not an error if defaults
// suffice; in that case an empty config is returned.
//
// Env overlay pattern (preserved from the pre-PR-#678 inline reads):
// main.go calls LoadConfig first, then re-applies FAAS_APID_* /
// FAAS_GITHUBD_* env vars on top via the Get helpers (ListenAddr()
// etc.). The env-var precedence over TOML is load-bearing for the
// containerised-deploys path (no TOML in those images).
func LoadConfig(path string) (*Config, error) {
	c := &Config{
		// Defaults match the legacy FAAS_APID_LISTEN and
		// FAAS_GITHUBD_SOCKET values so a partial / missing
		// toml behaves the same as the pre-PR-#678 inline-env
		// path. PR-0 is behaviour-preserving; these defaults
		// keep single-box dev booting unchanged.
		//
		// AdvisorySock + GithubdBridgeSock are intentionally NOT
		// defaulted here — pre-PR-0 the corresponding env vars
		// were the only source, and an empty value disabled the
		// listener. Setting non-empty defaults would auto-enable
		// the listeners on every e2e test, and the unix-socket
		// bind fails in CI (the per-daemon unix user `faas-apid`
		// / `faas-githubd` doesn't exist in the test container).
		ListenAddr:     "127.0.0.1:8081",
		GithubdSocket:  "/run/faas/githubd.sock",
		AppsDomain:     "gregale.dev",
		CLIAuthURLBase: defaultCLIAuthURLBase,
		// Issue #995 Phase 1: seed the timeout / header defaults
		// so a partial toml still produces the hardened listener.
		// The GetRequest*Timeout helpers fall back to
		// api.APID*SecondsDefault when the field is zero, so this
		// seed is belt-and-braces — the runtime defaults hold even
		// if these literals drift.
		RequestReadTimeout:    time.Duration(api.APIDReadTimeoutSecondsDefault) * time.Second,
		RequestWriteTimeout:   time.Duration(api.APIDWriteTimeoutSecondsDefault) * time.Second,
		RequestIdleTimeout:    time.Duration(api.APIDIdleTimeoutSecondsDefault) * time.Second,
		RequestMaxHeaderBytes: api.DefaultMaxHeaderBytes,
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			overlayAppErrorsTLSFromEnv(c)
			// Gate-B: even on the missing-file path, resolve Role
			// against FAAS_APID_ROLE so env wins over the empty
			// TOML default. role.FromConfig falls back to
			// RoleSingleBox when the env is unset.
			c.Role = role.FromConfig(string(c.Role), "FAAS_APID_ROLE")
			return c, nil
		}
		return nil, fmt.Errorf("apid: read %q: %w", path, err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("apid: parse %q: %w", path, err)
	}
	overlayAppErrorsTLSFromEnv(c)
	// Gate-B: resolve Role AFTER toml.Unmarshal so the post-decode
	// c.Role is consulted against FAAS_APID_ROLE. Setting Role in
	// the defaults-struct literal lets toml.Unmarshal overwrite it,
	// which would silently make the env override dead. The role
	// gate at boot calls role.Require to refuse to start under
	// the wrong box shape.
	c.Role = role.FromConfig(string(c.Role), "FAAS_APID_ROLE")
	// Mega-PR-A (issue #911 / ADR-110 PR-1): env-var overlay for
	// NodeName so the systemd drop-in (deploy/ansible/roles/
	// control_plane_service/templates/99-faas-node-name.conf.j2)
	// can override the TOML node_name on every control-plane box.
	// Empty keeps the TOML value (single-box dev back-compat).
	if v := os.Getenv("FAAS_NODE_NAME"); v != "" {
		c.NodeName = v
	}
	return c, nil
}

// overlayAppErrorsTLSFromEnv keeps the generated split-box systemd drop-in
// independent from hand-maintained apid.toml files. Empty values do not
// override TOML, preserving the single-box defaults and test fixtures.
func overlayAppErrorsTLSFromEnv(c *Config) {
	if c == nil {
		return
	}
	if v := os.Getenv("FAAS_APID_APP_ERRORS_TLS_CERT_PATH"); v != "" {
		c.AppErrorsTLSCertPath = v
	}
	if v := os.Getenv("FAAS_APID_APP_ERRORS_TLS_KEY_PATH"); v != "" {
		c.AppErrorsTLSKeyPath = v
	}
	if v := os.Getenv("FAAS_APID_APP_ERRORS_TLS_CA_PATH"); v != "" {
		c.AppErrorsTLSCAPath = v
	}
}

// GetListenAddr returns the listen address with env-var overlay
// (FAAS_APID_LISTEN wins over TOML). Single-box default 127.0.0.1:8081
// is the legacy loopback bind.
func (c *Config) GetListenAddr(env func(string) string) string {
	if v := env("FAAS_APID_LISTEN"); v != "" {
		return v
	}
	return c.ListenAddr
}

// GetMetricsAddr returns the metrics bind address with env-var
// overlay (FAAS_APID_METRICS_ADDR wins over TOML). Empty disables the
// listener (the scrape observer stays wired; only the bind is skipped).
func (c *Config) GetMetricsAddr(env func(string) string) string {
	if v := env("FAAS_APID_METRICS_ADDR"); v != "" {
		return v
	}
	return c.MetricsAddr
}

// GetAdvisorySock returns the advisory socket path with env-var
// overlay (FAAS_APID_ADVISORY_SOCK wins over TOML). Empty disables
// the listener. Mirrors the legacy resolveAdvisorySock helper in
// cmd/apid/main.go that this PR replaces.
func (c *Config) GetAdvisorySock(env func(string) string) string {
	if v := env("FAAS_APID_ADVISORY_SOCK"); v != "" {
		return v
	}
	return c.AdvisorySock
}

// GetGithubdBridgeSock returns the githubd bridge socket path with
// env-var overlay (FAAS_APID_GITHUBD_BRIDGE_SOCK wins over TOML).
// Empty disables. Mirrors the legacy resolveGithubdBridgeSock helper
// in cmd/apid/main.go that this PR replaces.
func (c *Config) GetGithubdBridgeSock(env func(string) string) string {
	if v := env("FAAS_APID_GITHUBD_BRIDGE_SOCK"); v != "" {
		return v
	}
	return c.GithubdBridgeSock
}

// GetAppErrorsTarget resolves the AppErrors listener target. The new target
// variable wins, while the historical socket variable remains an alias so
// existing single-box units do not need a coordinated config edit.
func (c *Config) GetAppErrorsTarget(env func(string) string) string {
	if v := env("FAAS_APID_APP_ERRORS_TARGET"); v != "" {
		return v
	}
	if v := env("FAAS_APID_APP_ERRORS_SOCKET"); v != "" {
		return v
	}
	if c != nil && c.AppErrorsTarget != "" {
		return c.AppErrorsTarget
	}
	return "/run/faas/app_errors.sock"
}

// GetGithubdSocket returns the githubd dial target with env-var
// overlay (FAAS_GITHUBD_SOCKET wins over TOML). Empty falls through
// to newGithubdClient's stub-client path (every method returns
// api.Problem{Code:"githubd_not_ready"}).
func (c *Config) GetGithubdSocket(env func(string) string) string {
	if v := env("FAAS_GITHUBD_SOCKET"); v != "" {
		return v
	}
	return c.GithubdSocket
}

// GetAppsDomain returns the apps-domain with env-var overlay
// (FAAS_APPS_DOMAIN wins over TOML). Empty disables wildcard
// routing in the dashboard login template.
func (c *Config) GetAppsDomain(env func(string) string) string {
	if v := env("FAAS_APPS_DOMAIN"); v != "" {
		return v
	}
	return c.AppsDomain
}

const defaultCLIAuthURLBase = "https://api.gregale.dev"

// GetCLIAuthURLBase returns the absolute API origin used in the URL returned
// by POST /v1/cli-auth/code. It is separate from GetAppsDomain because the
// dashboard wildcard host does not serve the API's /cli-auth route.
// FAAS_CLI_AUTH_URL_BASE wins over TOML; empty or malformed values fall back
// to the public API origin so the daemon never emits a relative or unusable
// browser URL.
func (c *Config) GetCLIAuthURLBase(env func(string) string) string {
	raw := ""
	if c != nil {
		raw = c.CLIAuthURLBase
	}
	if v := env("FAAS_CLI_AUTH_URL_BASE"); v != "" {
		raw = v
	}
	return normalizeCLIAuthURLBase(raw)
}

func normalizeCLIAuthURLBase(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultCLIAuthURLBase
	}
	// Keep the operator-facing config ergonomic while ensuring the API
	// response always contains an absolute URL.
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return defaultCLIAuthURLBase
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.TrimRight(u.String(), "/")
}

// GetRequestReadTimeout returns the http.Server.ReadTimeout with
// env-var overlay (FAAS_APID_REQUEST_READ_TIMEOUT wins over TOML).
// Zero falls back to api.APIDReadTimeoutSecondsDefault (60s —
// slowloris defence on the body arrival window). Issue #995 Phase 1
// / ADR-121.
func (c *Config) GetRequestReadTimeout(env func(string) string) time.Duration {
	if v := env("FAAS_APID_REQUEST_READ_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if c.RequestReadTimeout > 0 {
		return c.RequestReadTimeout
	}
	return time.Duration(api.APIDReadTimeoutSecondsDefault) * time.Second
}

// GetRequestWriteTimeout returns the http.Server.WriteTimeout with
// env-var overlay (FAAS_APID_REQUEST_WRITE_TIMEOUT wins over TOML).
// Zero falls back to api.APIDWriteTimeoutSecondsDefault (300s —
// matches gatewayd-internal ResponseWriteTimeoutDefault). Issue #995
// Phase 1 / ADR-121.
func (c *Config) GetRequestWriteTimeout(env func(string) string) time.Duration {
	if v := env("FAAS_APID_REQUEST_WRITE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if c.RequestWriteTimeout > 0 {
		return c.RequestWriteTimeout
	}
	return time.Duration(api.APIDWriteTimeoutSecondsDefault) * time.Second
}

// GetRequestIdleTimeout returns the http.Server.IdleTimeout with
// env-var overlay (FAAS_APID_REQUEST_IDLE_TIMEOUT wins over TOML).
// Zero falls back to api.APIDIdleTimeoutSecondsDefault (120s — bounds
// the keep-alive pool). Issue #995 Phase 1 / ADR-121.
func (c *Config) GetRequestIdleTimeout(env func(string) string) time.Duration {
	if v := env("FAAS_APID_REQUEST_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if c.RequestIdleTimeout > 0 {
		return c.RequestIdleTimeout
	}
	return time.Duration(api.APIDIdleTimeoutSecondsDefault) * time.Second
}

// GetRequestMaxHeaderBytes returns the http.Server.MaxHeaderBytes with
// env-var overlay (FAAS_APID_REQUEST_MAX_HEADER_BYTES wins over TOML).
// Zero falls back to api.DefaultMaxHeaderBytes (1 MiB). Issue #995
// Phase 1 / ADR-121.
func (c *Config) GetRequestMaxHeaderBytes(env func(string) string) int64 {
	if v := env("FAAS_APID_REQUEST_MAX_HEADER_BYTES"); v != "" {
		if n, err := parsePositiveInt(v); err == nil {
			return n
		}
	}
	if c.RequestMaxHeaderBytes > 0 {
		return c.RequestMaxHeaderBytes
	}
	return api.DefaultMaxHeaderBytes
}

// parsePositiveInt parses a non-negative integer string. Returns an
// error on non-digit input or overflow. Issue #995 Phase 1 helper.
func parsePositiveInt(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("parsePositiveInt: empty")
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("parsePositiveInt: %q", s)
		}
		// Standard idiomatic overflow check (see Knuth TAOCP §4.3.1,
		// or the Go strconv.ParseInt source). The pre-multiply bound
		// uses n > MaxInt64/10 so the case where the next digit is
		// exactly 7 still admits MaxInt64 (9223372036854775807).
		// The post-add check catches single-digit wraps when n is
		// exactly MaxInt64/10 and the next digit pushes it past.
		if n > math.MaxInt64/10 {
			return 0, fmt.Errorf("parsePositiveInt: overflow")
		}
		n = n*10 + int64(r-'0')
		if n < 0 {
			return 0, fmt.Errorf("parsePositiveInt: overflow")
		}
	}
	return n, nil
}

// LoadAdvisoryTLS returns the server mTLS config apid uses on the
// advisory listener (ADR-052). Empty cluster returns (nil, nil);
// partial cluster is rejected with the advisory_tls_* field names
// so an operator can map the error straight to a TOML key. Nil
// receiver tolerates the test seam (tests pass runDeps directly
// without setting preLoadedConfig; the nil-tolerance keeps the
// existing TestRunWithDeps_ListenErrorReturns shape working).
func (c *Config) LoadAdvisoryTLS() (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefix("advisory_", c.AdvisoryTLSCertPath, c.AdvisoryTLSKeyPath, c.AdvisoryTLSCAPath)
}

// LoadGithubdBridgeTLS returns the server mTLS config apid uses on
// the githubd → apid bridge listener (ADR-052). Empty cluster
// returns (nil, nil); partial cluster is rejected with the
// githubd_bridge_tls_* field names.
func (c *Config) LoadGithubdBridgeTLS() (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefix("githubd_bridge_", c.GithubdBridgeTLSCertPath, c.GithubdBridgeTLSKeyPath, c.GithubdBridgeTLSCAPath)
}

// LoadAppErrorsTLS returns the server mTLS config for the AppErrors listener.
// It intentionally follows the same empty-versus-partial path contract as
// the advisory and githubd bridge listeners.
func (c *Config) LoadAppErrorsTLS() (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefix("app_errors_", c.AppErrorsTLSCertPath, c.AppErrorsTLSKeyPath, c.AppErrorsTLSCAPath)
}

// LoadGithubdTLS returns the client mTLS config apid uses to dial
// githubd's EnqueueBuild gRPC server (ADR-052). Empty cluster returns
// (nil, nil); partial cluster is rejected with the githubd_tls_*
// field names.
func (c *Config) LoadGithubdTLS() (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadClientTLSConfigWithPrefix("githubd_", c.GithubdClientTLSCertPath, c.GithubdClientTLSKeyPath, c.GithubdClientTLSCAPath)
}

// LoadAdvisoryTLSWithVerifier is the PR-B (issue #678 / ADR-056) variant
// of LoadAdvisoryTLS. When v is non-nil, installs a
// tls.Config.VerifyPeerCertificate hook that consults v.LookupCN on the
// verified leaf-CN after the stdlib chain/SAN/EKU check passes. When v
// is nil (single-box dev / pre-PR-B wiring) the helper degrades to
// LoadAdvisoryTLS — no hook installed.
//
// PR-B replaces the PR-A plan: instead of a small leading slice that
// only adds *WithVerifier variants, the verifier-bearing helpers land
// here AND the PGNodeVerifier construction + dial-site threading land
// in cmd/apid/main.go as one combined PR. cmd/schedd already had
// LoadServerTLSWithVerifier / LoadVMMTLSWithVerifier (issue #678 PR-A
// in schedd's slice); apid's three Load*TLSWithVerifier siblings
// mirror that shape.
func (c *Config) LoadAdvisoryTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefixAndVerifier("advisory_", c.AdvisoryTLSCertPath, c.AdvisoryTLSKeyPath, c.AdvisoryTLSCAPath, v)
}

// LoadAdvisoryTLSWithPrefixAndVerifierAndReload is the ADR-052 §5
// / PR-E variant of LoadAdvisoryTLSWithVerifier. Same nil-tolerance
// contract: nil verifier / nil reload degrade to the no-hook /
// no-callback shape (LoadAdvisoryTLS).
func (c *Config) LoadAdvisoryTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefixAndVerifierAndReload("advisory_", c.AdvisoryTLSCertPath, c.AdvisoryTLSKeyPath, c.AdvisoryTLSCAPath, v, reload)
}

// LoadGithubdBridgeTLSWithVerifier is the PR-B (issue #678 / ADR-056)
// variant of LoadGithubdBridgeTLS. Same nil-verifier degradation
// contract as LoadAdvisoryTLSWithVerifier.
func (c *Config) LoadGithubdBridgeTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefixAndVerifier("githubd_bridge_", c.GithubdBridgeTLSCertPath, c.GithubdBridgeTLSKeyPath, c.GithubdBridgeTLSCAPath, v)
}

// LoadGithubdBridgeTLSWithPrefixAndVerifierAndReload is the
// ADR-052 §5 / PR-E variant of LoadGithubdBridgeTLSWithVerifier.
// Same nil-tolerance contract as LoadAdvisoryTLSWithPrefixAndVerifierAndReload.
func (c *Config) LoadGithubdBridgeTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefixAndVerifierAndReload("githubd_bridge_", c.GithubdBridgeTLSCertPath, c.GithubdBridgeTLSKeyPath, c.GithubdBridgeTLSCAPath, v, reload)
}

// LoadAppErrorsTLSWithPrefixAndVerifierAndReload combines mTLS, the
// compute-node CN binding, and SIGHUP-driven leaf rotation for the remote
// AppErrors listener. Unix-socket callers keep the nil-TLS path.
func (c *Config) LoadAppErrorsTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadServerTLSConfigWithPrefixAndVerifierAndReload("app_errors_", c.AppErrorsTLSCertPath, c.AppErrorsTLSKeyPath, c.AppErrorsTLSCAPath, v, reload)
}

// LoadGithubdTLSWithVerifier is the PR-B (issue #678 / ADR-056)
// variant of LoadGithubdTLS — the client-side mirror used to dial
// githubd's EnqueueBuild gRPC server. Same nil-verifier degradation
// contract as LoadAdvisoryTLSWithVerifier.
func (c *Config) LoadGithubdTLSWithVerifier(v wire.NodeVerifier) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadClientTLSConfigWithPrefixAndVerifier("githubd_", c.GithubdClientTLSCertPath, c.GithubdClientTLSKeyPath, c.GithubdClientTLSCAPath, v)
}

// LoadGithubdTLSWithPrefixAndVerifierAndReload is the
// ADR-052 §5 / PR-E variant of LoadGithubdTLSWithVerifier.
// Client-side: trust root is fixed at config-build time per
// ADR-052 §Risks "CA rotation pain"; only the leaf rotates
// per-handshake via stdlib's GetClientCertificate callback.
func (c *Config) LoadGithubdTLSWithPrefixAndVerifierAndReload(v wire.NodeVerifier, reload wire.ReloadFunc) (*tls.Config, error) {
	if c == nil {
		return nil, nil
	}
	return wire.LoadClientTLSConfigWithPrefixAndVerifierAndReload("githubd_", c.GithubdClientTLSCertPath, c.GithubdClientTLSKeyPath, c.GithubdClientTLSCAPath, v, reload)
}
