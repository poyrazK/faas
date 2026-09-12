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
)

type Config struct {
	ListenAddr   string                       `toml:"listen_addr"`
	DBURL        string                       `toml:"db_url"`
	MaxBodyBytes int64                        `toml:"max_body_bytes"`
	ReadTimeout  time.Duration                `toml:"read_timeout"`
	WriteTimeout time.Duration                `toml:"write_timeout"`
	IdleTimeout  time.Duration                `toml:"idle_timeout"`
	Integrations map[string]IntegrationConfig `toml:"integrations"`
}

type IntegrationConfig struct {
	ID             string        `toml:"id"`
	AccountID      string        `toml:"account_id"`
	Name           string        `toml:"name"`
	Origin         string        `toml:"origin"`
	TokenEnv       string        `toml:"token_env"`
	AppIDs         []string      `toml:"app_ids"`
	RatePerSecond  float64       `toml:"rate_per_second"`
	Burst          int           `toml:"burst"`
	MaxInFlight    int           `toml:"max_in_flight"`
	RequestTimeout time.Duration `toml:"request_timeout"`
	Enabled        *bool         `toml:"enabled"`
}

func LoadConfig(path string) (*Config, error) {
	c := &Config{
		ListenAddr:   "127.0.0.1:8095",
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
	Record outbound.IntegrationRecord
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
		timeout := raw.RequestTimeout
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		policy, err := outbound.NewIntegration(id, raw.Origin, token, raw.AppIDs, raw.RatePerSecond, raw.Burst, raw.MaxInFlight, timeout)
		if err != nil {
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
		out = append(out, configuredIntegration{Record: outbound.IntegrationRecord{AccountID: accountID, Name: name, Policy: policy}})
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
