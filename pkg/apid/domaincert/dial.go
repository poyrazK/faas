// Package domaincert connects certificate probes to prevalidated addresses.
package domaincert

import (
	"context"
	"errors"
	"net"
	"time"
)

// Dial tries every numeric address without resolving the hostname again.
// Each remaining address gets a share of the remaining time, so a blackholed
// first route cannot consume the entire probe budget. Callers validate the
// complete answer set before invoking Dial.
func Dial(ctx context.Context, addresses []net.IPAddr, timeout time.Duration, dial func(context.Context, string, string) (net.Conn, error)) (net.Conn, error) {
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
		stop()
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}
