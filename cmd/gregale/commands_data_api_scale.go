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

// dataAPIScale is dataAPIScaleSettings parsed once; a zero count with its
// setting given means "default" or "off".
type dataAPIScale struct {
	dataAPIScaleSettings
	pool, serverKeyConcurrency int
	ratePerSecond, rateBurst   int
}

// parseDataAPIScale checks the flags before anything is written.
func parseDataAPIScale(s dataAPIScaleSettings) (dataAPIScale, error) {
	p := dataAPIScale{dataAPIScaleSettings: s}
	var err error
	if s.Pool != "" {
		if p.pool, _, err = dataAPICount("pool", s.Pool, "default", api.DataAPIMaxPoolConnections); err != nil {
			return p, err
		}
	}
	if s.ServerKeyConcurrency != "" {
		if p.serverKeyConcurrency, _, err = dataAPICount("server-key-concurrency", s.ServerKeyConcurrency, "default", api.DataAPIMaxPoolConnections); err != nil {
			return p, err
		}
	}
	if s.ServerKeyRateLimit != "" {
		if p.ratePerSecond, p.rateBurst, _, err = parseDataAPIServerKeyRate(s.ServerKeyRateLimit); err != nil {
			return p, err
		}
	}
	return p, nil
}

// configureDataAPIScale stores the capacity settings. The runtime treats a
// per-user or server-key limit above the engine pool as the pool, so no pool
// change can stop it from starting.
func configureDataAPIScale(ctx context.Context, client *api.Client, app, scope string, p dataAPIScale, receipt *dataAPIConfigureReceipt) error {
	set := func(key, value string) error { return client.SetSecretWithScope(ctx, app, key, value, scope) }
	unset := func(key string) error { return unsetDataAPISetting(ctx, client, app, key, scope) }
	if p.Pool != "" {
		if err := configureDataAPIPool(ctx, client, app, scope, p.pool, set, unset); err != nil {
			return err
		}
		receipt.Pool = p.Pool
		receipt.Changed = append(receipt.Changed, "pool")
	}
	if p.ServerKeyConcurrency != "" {
		var err error
		if p.serverKeyConcurrency > 0 {
			err = set(dataAPIServerKeyConcurrencyKey, strconv.Itoa(p.serverKeyConcurrency))
		} else {
			err = unset(dataAPIServerKeyConcurrencyKey)
		}
		if err != nil {
			return err
		}
		receipt.ServerKeyConcurrency = p.ServerKeyConcurrency
		receipt.Changed = append(receipt.Changed, "server_key_concurrency")
	}
	if p.ServerKeyRateLimit != "" {
		if err := configureDataAPIServerKeyRate(p.ratePerSecond, p.rateBurst, set, unset); err != nil {
			return err
		}
		receipt.ServerKeyRateLimit = p.ServerKeyRateLimit
		receipt.Changed = append(receipt.Changed, "server_key_rate_limit")
	}
	return nil
}

// configureDataAPIPool stores the pool, or removes it for 0. A one-connection
// pool cannot keep a coordinated-refresh reader, so it is refused before
// writing; the runtime accepts DATA_API_COORDINATED_REFRESH only as 1, so the
// key's presence means refresh is on.
func configureDataAPIPool(ctx context.Context, client *api.Client, app, scope string, pool int, set func(string, string) error, unset func(string) error) error {
	if pool == 1 {
		secrets, err := client.ListSecretsWithScope(ctx, app, scope)
		if err != nil {
			return err
		}
		for _, secret := range secrets.Secrets {
			if secret.Key == "DATA_API_COORDINATED_REFRESH" && (secret.Scope == "" || secret.Scope == scope) {
				return errors.New("coordinated refresh keeps one connection for its reader; use --pool 2 or more")
			}
		}
	}
	if pool == 0 {
		return unset(dataAPIPoolKey)
	}
	return set(dataAPIPoolKey, strconv.Itoa(pool))
}

// configureDataAPIServerKeyRate writes the new rate before removing anything,
// so a failed write never leaves server keys without the limit they had. Off
// removes the rate first: a burst left alone still limits at the default rate.
func configureDataAPIServerKeyRate(perSecond, burst int, set func(string, string) error, unset func(string) error) error {
	if perSecond == 0 {
		if err := unset(dataAPIServerKeyRateKey); err != nil {
			return err
		}
		return unset(dataAPIServerKeyBurstKey)
	}
	if err := set(dataAPIServerKeyRateKey, strconv.Itoa(perSecond)); err != nil {
		return err
	}
	if burst > 0 {
		return set(dataAPIServerKeyBurstKey, strconv.Itoa(burst))
	}
	return unset(dataAPIServerKeyBurstKey)
}
