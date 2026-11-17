package commit

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/publicdest"
)

// ErrConnectionAuthentication preserves a useful classification without
// retaining server messages, addresses, SQL, or connection credentials.
var ErrConnectionAuthentication = errors.New("commit: database authentication unavailable")

func connectionFailure(err error) error {
	var authentication *pgconn.PgError
	if errors.As(err, &authentication) && (authentication.Code == "28P01" || authentication.Code == "28000") {
		return ErrConnectionAuthentication
	}
	return errors.New("commit: database connection unavailable")
}

// NetworkPolicy is operator-owned, never populated from customer payloads.
// Exact hostnames prevent using credential registration as an arbitrary
// network probe; address prefixes authorize each freshly resolved destination.
type NetworkPolicy struct {
	Hosts    map[string]bool
	Prefixes []netip.Prefix
	// PublicInternet additionally admits any host name that is not in Hosts
	// when every address it resolves to is globally reachable
	// (ADR-20261112140000 §6). The relay sets it only for changes sources,
	// and only when the operator enables FAAS_COMMIT_PUBLIC_DATABASES.
	PublicInternet bool
}

// publicHost reports whether host is governed by the public-internet rule
// rather than the operator allowlist.
func (p NetworkPolicy) publicHost(host string) bool {
	return p.PublicInternet && !p.Hosts[host] && host != ""
}

func (p NetworkPolicy) permits(host string, ip netip.Addr) bool {
	if p.publicHost(host) {
		return publicdest.IsPublic(ip)
	}
	if !p.Hosts[host] {
		return false
	}
	ip = ip.Unmap()
	if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return false
	}
	for _, prefix := range p.Prefixes {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// OpenPool bounds customer connections and enforces policy on every dial,
// including reconnection and DNS changes. TLS verifies the original hostname.
func OpenPool(ctx context.Context, raw string, policy NetworkPolicy) (*pgxpool.Pool, error) {
	if err := ValidateConnection(raw); err != nil {
		return nil, err
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		return nil, errors.New("commit: invalid database connection")
	}
	public := policy.publicHost(cfg.ConnConfig.Host)
	if !public && (!policy.Hosts[cfg.ConnConfig.Host] || len(policy.Prefixes) == 0) {
		return nil, errors.New("commit: database destination not qualified")
	}
	// A literal address bypasses no checks, but a customer database must be
	// named: TLS verifies the host name.
	if _, err := netip.ParseAddr(cfg.ConnConfig.Host); public && err == nil {
		return nil, errors.New("commit: database destination must be a host name")
	}
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	cfg.ConnConfig.RuntimeParams["application_name"] = "gregale-commit"
	// The first release supports the public outbox schema. Customer-controlled
	// role defaults cannot redirect the relay to a different same-named table.
	cfg.ConnConfig.RuntimeParams["search_path"] = "public"
	qualifiedHost := cfg.ConnConfig.Host
	// pgx resolves the hostname before invoking DialFunc. Qualify the original
	// hostname here and re-resolve it on every connection, without replacing
	// TLS's original ServerName with an IP address.
	cfg.ConnConfig.LookupFunc = func(ctx context.Context, host string) ([]string, error) {
		if host != qualifiedHost || (!policy.Hosts[host] && !policy.publicHost(host)) {
			return nil, errors.New("commit: database destination not qualified")
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("commit: database DNS unavailable")
		}
		allowed := make([]string, 0, len(addresses))
		for _, ip := range addresses {
			if policy.permits(host, ip) {
				allowed = append(allowed, ip.Unmap().String())
			} else if policy.publicHost(host) {
				// A public name must resolve only to public addresses: a mixed
				// answer is how DNS rebinding reaches a private network.
				return nil, errors.New("commit: database destination not qualified")
			}
		}
		if len(allowed) == 0 {
			return nil, errors.New("commit: qualified database destination unavailable")
		}
		return allowed, nil
	}
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("commit: invalid database destination")
		}
		ip, err := netip.ParseAddr(host)
		if err != nil || !policy.permits(qualifiedHost, ip) {
			return nil, errors.New("commit: database address not qualified")
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err != nil {
			return nil, errors.New("commit: qualified database destination unavailable")
		}
		return conn, nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("commit: database pool unavailable")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, connectionFailure(err)
	}
	return pool, nil
}
