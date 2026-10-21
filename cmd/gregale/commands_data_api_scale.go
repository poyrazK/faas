// adr: 20261021180534 — engine pool size and server-key limits as configure
// settings.
package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	dataAPIPoolKey                 = "DATA_API_POOL"
	dataAPIServerKeyConcurrencyKey = "DATA_API_SERVER_KEY_CONCURRENCY"
	dataAPIServerKeyRateKey        = "DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND"
	dataAPIServerKeyBurstKey       = "DATA_API_SERVER_KEY_RATE_LIMIT_BURST"
)

// dataAPIScaleSettings are the configure flags for capacity; empty means
// unchanged, and "default" or "off" removes the setting.
type dataAPIScaleSettings struct {
	Pool                 string
	ServerKeyConcurrency string
	ServerKeyRateLimit   string
}

func (s dataAPIScaleSettings) empty() bool {
	return s.Pool == "" && s.ServerKeyConcurrency == "" && s.ServerKeyRateLimit == ""
}

// dataAPICount reads an integer from 1 to limit, or the reset word.
func dataAPICount(name, value, reset string, limit int) (int, bool, error) {
	if value == reset {
		return 0, true, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > limit || strconv.Itoa(n) != value {
		return 0, false, fmt.Errorf("--%s must be %s or a whole number from 1 to %d", name, reset, limit)
	}
	return n, false, nil
}

// parseDataAPIServerKeyRate reads N or N,burst=M, or off.
func parseDataAPIServerKeyRate(value string) (perSecond, burst int, off bool, err error) {
	if value == "off" {
		return 0, 0, true, nil
	}
	rate, rest, hasBurst := strings.Cut(value, ",")
	invalid := fmt.Errorf("--server-key-rate-limit must be off, N or N,burst=M with N from 1 to %d and M from 1 to %d", api.DataAPIRateLimitMaxPerSecond, api.DataAPIRateLimitMaxBurst)
	if perSecond, _, err = dataAPICount("server-key-rate-limit", rate, "off", api.DataAPIRateLimitMaxPerSecond); err != nil {
		return 0, 0, false, invalid
	}
	if !hasBurst {
		return perSecond, 0, false, nil
	}
	value, ok := strings.CutPrefix(rest, "burst=")
	if burst, _, err = dataAPICount("server-key-rate-limit", value, "off", api.DataAPIRateLimitMaxBurst); !ok || err != nil {
		return 0, 0, false, invalid
	}
	return perSecond, burst, false, nil
}

// validateDataAPIScale checks the flags before anything is written.
func validateDataAPIScale(s dataAPIScaleSettings) error {
	if s.Pool != "" {
		if _, _, err := dataAPICount("pool", s.Pool, "default", api.DataAPIMaxPoolConnections); err != nil {
			return err
		}
	}
	if s.ServerKeyConcurrency != "" {
		if _, _, err := dataAPICount("server-key-concurrency", s.ServerKeyConcurrency, "default", api.DataAPIMaxPoolConnections); err != nil {
			return err
		}
	}
	if s.ServerKeyRateLimit != "" {
		if _, _, _, err := parseDataAPIServerKeyRate(s.ServerKeyRateLimit); err != nil {
			return err
		}
	}
	return nil
}

// configureDataAPIScale stores the capacity settings. A one-connection pool
// cannot keep a coordinated-refresh reader, so it is refused before writing.
func configureDataAPIScale(ctx context.Context, client *api.Client, app, scope string, s dataAPIScaleSettings, receipt *dataAPIConfigureReceipt) ([]string, error) {
	var warnings []string
	if s.empty() {
		return nil, nil
	}
	secrets, err := client.ListSecretsWithScope(ctx, app, scope)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, secret := range secrets.Secrets {
		if secret.Scope == "" || secret.Scope == scope {
			present[secret.Key] = true
		}
	}
	set := func(key, value string) error { return client.SetSecretWithScope(ctx, app, key, value, scope) }
	unset := func(key string) error { return unsetDataAPISetting(ctx, client, app, key, scope) }
	if s.Pool != "" {
		pool, reset, _ := dataAPICount("pool", s.Pool, "default", api.DataAPIMaxPoolConnections)
		if pool == 1 && present["DATA_API_COORDINATED_REFRESH"] {
			return nil, errors.New("coordinated refresh keeps one connection for its reader; use --pool 2 or more")
		}
		if reset {
			err = unset(dataAPIPoolKey)
		} else {
			err = set(dataAPIPoolKey, s.Pool)
		}
		if err != nil {
			return nil, err
		}
		if present["DATA_API_USER_CONCURRENCY"] {
			warnings = append(warnings, "DATA_API_USER_CONCURRENCY is set: keep it at or below the engine pool, or the runtime refuses to start")
		}
		receipt.Pool = s.Pool
		receipt.Changed = append(receipt.Changed, "pool")
	}
	if s.ServerKeyConcurrency != "" {
		if s.ServerKeyConcurrency == "default" {
			err = unset(dataAPIServerKeyConcurrencyKey)
		} else {
			err = set(dataAPIServerKeyConcurrencyKey, s.ServerKeyConcurrency)
		}
		if err != nil {
			return nil, err
		}
		receipt.ServerKeyConcurrency = s.ServerKeyConcurrency
		receipt.Changed = append(receipt.Changed, "server_key_concurrency")
	}
	if s.ServerKeyRateLimit != "" {
		perSecond, burst, off, _ := parseDataAPIServerKeyRate(s.ServerKeyRateLimit)
		for _, key := range []string{dataAPIServerKeyRateKey, dataAPIServerKeyBurstKey} {
			if err = unset(key); err != nil {
				return nil, err
			}
		}
		if !off {
			if err = set(dataAPIServerKeyRateKey, strconv.Itoa(perSecond)); err == nil && burst > 0 {
				err = set(dataAPIServerKeyBurstKey, strconv.Itoa(burst))
			}
			if err != nil {
				return nil, err
			}
		}
		receipt.ServerKeyRateLimit = s.ServerKeyRateLimit
		receipt.Changed = append(receipt.Changed, "server_key_rate_limit")
	}
	return warnings, nil
}
