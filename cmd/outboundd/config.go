package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/outbound"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type Config struct {
	ListenAddr string `toml:"listen_addr"`
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
	ID                       string        `toml:"id"`
	AccountID                string        `toml:"account_id"`
	Name                     string        `toml:"name"`
	Origin                   string        `toml:"origin"`
	TokenEnv                 string        `toml:"token_env"`
	ProviderAuthorizationEnv string        `toml:"provider_authorization_env"`
	CredentialSource         string        `toml:"credential_source"`
	AllowedMethods           []string      `toml:"allowed_methods"`
	AllowedPathPrefixes      []string      `toml:"allowed_path_prefixes"`
	AppIDs                   []string      `toml:"app_ids"`
	RatePerSecond            float64       `toml:"rate_per_second"`
	Burst                    int           `toml:"burst"`
	MaxInFlight              int           `toml:"max_in_flight"`
	RequestTimeout           time.Duration `toml:"request_timeout"`
	Enabled                  *bool         `toml:"enabled"`
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
