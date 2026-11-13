// Package domaincert connects certificate probes to prevalidated addresses.
package domaincert

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"
)

// Dial tries every numeric address without resolving the hostname again.
// Each remaining address gets a share of the remaining time, so a blackholed
// first route cannot consume the entire probe budget. Callers validate the
// complete answer set before invoking Dial.
func Dial(ctx context.Context, addresses []net.IPAddr, timeout time.Duration, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
	return dialAndCheck(ctx, addresses, timeout, dial, nil)
}

// DialTLS validates each TLS handshake within its share of the probe budget.
// A failed handshake closes that connection before trying the next address.
func DialTLS(ctx context.Context, addresses []net.IPAddr, timeout time.Duration, dial func(context.Context, string, string) (net.Conn, error), config *tls.Config) (*tls.Conn, error) {
	conn, err := dialAndCheck(ctx, addresses, timeout, dial, func(ctx context.Context, raw net.Conn) (net.Conn, error) {
		client := tls.Client(raw, config)
		if err := client.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		return client, nil
	})
	if err != nil {
		return nil, err
	}
	return conn.(*tls.Conn), nil
}

func dialAndCheck(ctx context.Context, addresses []net.IPAddr, timeout time.Duration, dial func(context.Context, string, string) (net.Conn, error), check func(context.Context, net.Conn) (net.Conn, error)) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	last := errors.New("no certificate addresses")
	for index, address := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt, stop := context.WithTimeout(ctx, time.Until(deadline)/time.Duration(len(addresses)-index))
		conn, err := dial(attempt, "tcp", net.JoinHostPort(address.IP.String(), "443"))
		if err == nil && check != nil {
			var checked net.Conn
			checked, err = check(attempt, conn)
			if err != nil {
				_ = conn.Close()
			} else {
				conn = checked
			}
		}
		stop()
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}
