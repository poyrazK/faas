package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type Config struct {
	WorkflowOutboundEnabled bool   `toml:"workflow_outbound_enabled"`
	ListenAddr              string `toml:"listen_addr"`
	// MetricsAddr is the private bind address for the operator-only
	// Prometheus endpoint. Keep it loopback unless a firewall explicitly
	// restricts the scrape network.
	MetricsAddr              string                       `toml:"metrics_addr"`
	DBURL                    string                       `toml:"db_url"`
	MaxBodyBytes             int64                        `toml:"max_body_bytes"`
	ReadTimeout              time.Duration                `toml:"read_timeout"`
	WriteTimeout             time.Duration                `toml:"write_timeout"`
	IdleTimeout              time.Duration                `toml:"idle_timeout"`
	Integrations             map[string]IntegrationConfig `toml:"integrations"`
	WorkloadIdentityJWKSPath string                       `toml:"workload_identity_jwks_path"`
	WorkloadIdentityIssuer   string                       `toml:"workload_identity_issuer"`
	// RelayListen enables the app relay listener for vmmd (ADR-989):
	// "unix:///run/faas/outboundd-relay.sock" on a single box, or
	// "tcp://host:port" with the relay_tls_* mTLS material on a split box.
	RelayListen      string `toml:"relay_listen"`
	RelayTLSCertPath string `toml:"relay_tls_cert_path"`
	RelayTLSKeyPath  string `toml:"relay_tls_key_path"`
	RelayTLSCAPath   string `toml:"relay_tls_ca_path"`
	// AIGatewayEnabled is the AI gateway release gate (ADR-991). While it is
	// off, AI integrations and routes are refused. FAAS_AI_GATEWAY_ENABLED
	// overrides it: "1" turns it on, any other value turns it off.
	AIGatewayEnabled bool `toml:"ai_gateway_enabled"`
	// AIGatewayAccounts limits customer AI integrations to these account IDs
	// during a staged preview rollout (ADR-998); empty or "*" admits every
	// entitled account. FAAS_AI_GATEWAY_ACCOUNTS overrides it.
	AIGatewayAccounts []string `toml:"ai_gateway_accounts"`
	// PublicListenAddr enables the AI gateway's public listener for callers
	// outside Gregale that present an access token (ADR-1431). It must be a
	// loopback address: Caddy terminates TLS in front of it.
	PublicListenAddr string `toml:"public_listen_addr"`
}

// validatePublicListen refuses a public listener that is not on loopback.
func (c *Config) validatePublicListen() error {
	if c.PublicListenAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(c.PublicListenAddr)
	if err != nil {
		return fmt.Errorf("outboundd: public_listen_addr: %w", err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("outboundd: public_listen_addr %q must be a loopback IP address behind the TLS proxy", c.PublicListenAddr)
	}
	return nil
}

// AIGatewayRollout resolves the staged rollout from the TOML setting and the
// environment override (ADR-998).
func (c *Config) AIGatewayRollout(getenv func(string) string) (outbound.AIGatewayRollout, error) {
	override := ""
	if getenv != nil {
		override = getenv("FAAS_AI_GATEWAY_ACCOUNTS")
	}
	return outbound.ParseAIGatewayRollout(c.AIGatewayAccounts, override)
}

// AIGateway resolves the release gate from the TOML setting and the
// environment override.
func (c *Config) AIGateway(getenv func(string) string) bool {
	if getenv != nil {
		if value := getenv("FAAS_AI_GATEWAY_ENABLED"); value != "" {
			return value == "1"
		}
	}
	return c.AIGatewayEnabled
}

// ValidateAIGateway refuses an operator integration that declares an AI
// provider while the AI gateway is off, so a configuration mistake fails at
// startup instead of refusing that integration's traffic.
func ValidateAIGateway(items []configuredIntegration, enabled bool) error {
	if enabled {
		return nil
	}
	for _, item := range items {
		if item.Record.Policy.AIProvider != "" {
			return fmt.Errorf("outboundd: integration %q sets ai_provider but the AI gateway is disabled; set ai_gateway_enabled or FAAS_AI_GATEWAY_ENABLED=1", item.Record.Policy.ID)
		}
	}
	return nil
}

func (c *Config) IdentityVerifier(items []configuredIntegration) (outbound.IdentityVerifier, error) {
	managed := false
	for _, item := range items {
		if item.Record.Policy.ProviderAuthMode == outbound.ProviderAuthManaged {
			managed = true
			break
		}
	}
	if !managed && c.WorkloadIdentityJWKSPath == "" {
		return nil, nil
	}
	if c.WorkloadIdentityJWKSPath == "" {
		return nil, errors.New("managed outbound integrations require workload_identity_jwks_path")
	}
	data, err := os.ReadFile(c.WorkloadIdentityJWKSPath)
	if err != nil {
		return nil, fmt.Errorf("read outbound workload identity JWKS: %w", err)
	}
	issuer := c.WorkloadIdentityIssuer
	if issuer == "" {
		issuer = workloadidentity.DefaultIssuer
	}
	verifier, err := outbound.NewWorkloadIdentityVerifier(data, issuer)
	if err != nil {
		return nil, err
	}
	return verifier, nil
}

type IntegrationConfig struct {
	ID                             string        `toml:"id"`
	AccountID                      string        `toml:"account_id"`
	Name                           string        `toml:"name"`
	Origin                         string        `toml:"origin"`
	TokenEnv                       string        `toml:"token_env"`
	ProviderAuthorizationEnv       string        `toml:"provider_authorization_env"`
	CredentialSource               string        `toml:"credential_source"`
	AllowedMethods                 []string      `toml:"allowed_methods"`
	AllowedPathPrefixes            []string      `toml:"allowed_path_prefixes"`
	AppIDs                         []string      `toml:"app_ids"`
	RatePerSecond                  float64       `toml:"rate_per_second"`
	Burst                          int           `toml:"burst"`
	MaxInFlight                    int           `toml:"max_in_flight"`
	DailyRequestLimit              int64         `toml:"daily_request_limit"`
	RequestTimeout                 time.Duration `toml:"request_timeout"`
	MaxRetries                     int           `toml:"max_retries"`
	ResponseCacheTTLSeconds        int           `toml:"response_cache_ttl_seconds"`
	CircuitBreakerFailureThreshold int           `toml:"circuit_breaker_failure_threshold"`
	CircuitBreakerOpenSeconds      int           `toml:"circuit_breaker_open_seconds"`
	RetryBudgetPerMinute           int           `toml:"retry_budget_per_minute"`
	AIProvider                     string        `toml:"ai_provider"`
	AIDailyTokenLimit              int64         `toml:"ai_daily_token_limit"`
	// AIAllowUnmetered lets through writes the gateway cannot meter
	// (ADR-1432).
	AIAllowUnmetered bool `toml:"ai_allow_unmetered"`
	// AIAutoPromptCache adds prompt-cache breakpoints to Claude requests
	// (ADR-1437).
	AIAutoPromptCache bool  `toml:"ai_auto_prompt_cache"`
	Enabled           *bool `toml:"enabled"`
}

func LoadConfig(path string) (*Config, error) {
	c := &Config{
		ListenAddr:   "127.0.0.1:8095",
		MetricsAddr:  "127.0.0.1:9108",
		MaxBodyBytes: 25 << 20,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 2 * time.Minute,
		IdleTimeout:  2 * time.Minute,
		Integrations: make(map[string]IntegrationConfig),
	}
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, fmt.Errorf("outboundd: read config: %w", err)
	}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("outboundd: parse config: %w", err)
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8095"
	}
	if c.MetricsAddr == "" {
		c.MetricsAddr = "127.0.0.1:9108"
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = 25 << 20
	}
	if c.ReadTimeout <= 0 {
		c.ReadTimeout = 30 * time.Second
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 2 * time.Minute
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 2 * time.Minute
	}
	if c.Integrations == nil {
		c.Integrations = make(map[string]IntegrationConfig)
	}
	if err := c.validatePublicListen(); err != nil {
		return nil, err
	}
	return c, nil
}

type configuredIntegration struct {
	Record                outbound.IntegrationRecord
	providerAuthorization string
}

func (c *Config) Policies(getenv func(string) string) ([]configuredIntegration, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	out := make([]configuredIntegration, 0, len(c.Integrations))
	for key, raw := range c.Integrations {
		id := raw.ID
		if id == "" {
			id = key
		}
		if _, err := uuid.Parse(id); err != nil {
			return nil, fmt.Errorf("integration %q: id must be a UUID: %w", key, err)
		}
		accountID, err := uuid.Parse(raw.AccountID)
		if err != nil {
			return nil, fmt.Errorf("integration %q: account_id must be a UUID: %w", key, err)
		}
		tokenEnv := raw.TokenEnv
		if tokenEnv == "" {
			return nil, fmt.Errorf("integration %q: token_env is required", key)
		}
		token := getenv(tokenEnv)
		if token == "" {
			return nil, fmt.Errorf("integration %q: token environment variable %s is empty", key, tokenEnv)
		}
		credentialSource := raw.CredentialSource
		if credentialSource == "" {
			credentialSource = outbound.CredentialSourceOperatorEnv
		}
		if credentialSource != outbound.CredentialSourceOperatorEnv && credentialSource != outbound.CredentialSourceCustomerSealed {
			return nil, fmt.Errorf("integration %q: invalid credential_source", key)
		}
		if credentialSource == outbound.CredentialSourceCustomerSealed && raw.ProviderAuthorizationEnv != "" {
			return nil, fmt.Errorf("integration %q: customer_sealed cannot use provider_authorization_env", key)
		}
		var providerAuthorization string
		if raw.ProviderAuthorizationEnv != "" {
			if raw.ProviderAuthorizationEnv == tokenEnv {
				return nil, fmt.Errorf("integration %q: provider_authorization_env must differ from token_env", key)
			}
			providerAuthorization = getenv(raw.ProviderAuthorizationEnv)
			if !outbound.ValidManagedAuthorization(providerAuthorization) {
				return nil, fmt.Errorf("integration %q: provider_authorization_env is empty or invalid", key)
			}
		}
		timeout := raw.RequestTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		policy, err := outbound.NewIntegration(id, raw.Origin, token, raw.AppIDs, raw.RatePerSecond, raw.Burst, raw.MaxInFlight, timeout)
		if err != nil {
			return nil, fmt.Errorf("integration %q: %w", key, err)
		}
		if providerAuthorization != "" || credentialSource == outbound.CredentialSourceCustomerSealed {
			policy.ProviderAuthMode = outbound.ProviderAuthManaged
		}
		policy.CredentialSource = credentialSource
		policy.MaxRetries = raw.MaxRetries
		policy.ResponseCacheTTLSeconds = raw.ResponseCacheTTLSeconds
		policy.CircuitBreakerFailureThreshold = raw.CircuitBreakerFailureThreshold
		policy.CircuitBreakerOpenSeconds = raw.CircuitBreakerOpenSeconds
		policy.RetryBudgetPerMinute = raw.RetryBudgetPerMinute
		if raw.DailyRequestLimit != 0 {
			limit := raw.DailyRequestLimit
			policy.DailyRequestLimit = &limit
		}
		policy.AIProvider = raw.AIProvider
		if raw.AIDailyTokenLimit != 0 {
			limit := raw.AIDailyTokenLimit
			policy.DailyAITokenLimit = &limit
		}
		policy.AIAllowUnmetered = raw.AIAllowUnmetered
		policy.AIAutoPromptCache = raw.AIAutoPromptCache
		policy.AllowedMethods = raw.AllowedMethods
		policy.AllowedPathPrefixes = raw.AllowedPathPrefixes
		if err := policy.Validate(); err != nil {
			return nil, fmt.Errorf("integration %q: %w", key, err)
		}
		enabled := true
		if raw.Enabled != nil {
			enabled = *raw.Enabled
		}
		policy.Enabled = enabled
		name := raw.Name
		if name == "" {
			name = key
		}
		out = append(out, configuredIntegration{
			Record:                outbound.IntegrationRecord{AccountID: accountID, Name: name, Policy: policy},
			providerAuthorization: providerAuthorization,
		})
	}
	return out, nil
}

func defaultConfigPath() string {
	if f := flag.Lookup("config"); f != nil && strings.TrimSpace(f.Value.String()) != "" {
		return f.Value.String()
	}
	if path := os.Getenv("FAAS_OUTBOUNDD_CONFIG"); strings.TrimSpace(path) != "" {
		return path
	}
	return "/etc/faas/outboundd.toml"
}
