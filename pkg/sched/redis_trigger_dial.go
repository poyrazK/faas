package sched

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/onebox-faas/faas/pkg/oci"
)

// redisTriggerDialer is the dialer a Redis Streams trigger's client uses:
// the egress-guarded dial (tenant egress rules, no private or metadata
// addresses), then a TLS handshake when the trigger asks for TLS.
//
// go-redis negotiates TLS only in its own default dialer. With a custom
// Dialer set, Options.TLSConfig was ignored and a `tls: true` trigger sent
// AUTH and its records in plaintext (blocker hunt #6, 2026-11-01), so the
// handshake happens here, before go-redis writes anything.
func redisTriggerDialer(tlsConfig *tls.Config) func(context.Context, string, string) (net.Conn, error) {
	return redisTLSDial(oci.EgressDialContext(&net.Dialer{Timeout: 5 * time.Second}), tlsConfig)
}

// redisTLSDial wraps dial with a TLS handshake when tlsConfig is set.
func redisTLSDial(dial func(context.Context, string, string) (net.Conn, error), tlsConfig *tls.Config) func(context.Context, string, string) (net.Conn, error) {
	if tlsConfig == nil {
		return dial
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		raw, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		cfg := tlsConfig.Clone()
		if cfg.ServerName == "" {
			// Verify the name the trigger was configured with, not the
			// address the egress guard resolved it to.
			host, _, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				host = address
			}
			cfg.ServerName = host
		}
		conn := tls.Client(raw, cfg)
		if err := conn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, fmt.Errorf("redis_poller: tls handshake with %s: %w", address, err)
		}
		return conn, nil
	}
}

// redisTriggerOptions is a Redis Streams trigger's client configuration.
// TLSConfig is set too, for go-redis's own uses of it (endpoint detection),
// but the handshake is the dialer's.
func redisTriggerOptions(cfg redisConfig) *redis.Options {
	var tlsConfig *tls.Config
	if cfg.TLS {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return &redis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		Dialer:       redisTriggerDialer(tlsConfig),
		TLSConfig:    tlsConfig,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		// Poll, acknowledgements and lag reads share this per-trigger pool.
		PoolSize:              4,
		MaxActiveConns:        4,
		PoolTimeout:           time.Second,
		ContextTimeoutEnabled: true,
		MaxRetries:            2,
		MinRetryBackoff:       25 * time.Millisecond,
		MaxRetryBackoff:       500 * time.Millisecond,
	}
}
