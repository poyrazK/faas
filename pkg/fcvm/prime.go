package fcvm

// Init snapshot priming (ADR-20261114153000). Before schedd captures a
// deployment's first init snapshot, vmmd sends a short burst of GET requests
// to the guest's own HTTP port so the snapshot holds code paths that already
// ran once: JIT-compiled methods, loaded classes and modules, warmed pools.
// Priming changes no lifecycle state and never fails a capture; the caller
// snapshots whatever the result.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Priming limits. vmmd clamps whatever schedd asks for to these, so a
// mistaken caller cannot hold a capture open longer than the budget.
const (
	PrimeMaxRequests = 20
	PrimeMaxBudget   = 5 * time.Second
	// primeRequestTimeout bounds one GET. A handler slower than this is
	// left running in the guest; the next GET still starts.
	primeRequestTimeout = 2 * time.Second
)

// PrimeOptions is one priming burst.
type PrimeOptions struct {
	Path     string
	Requests int
	Budget   time.Duration
}

// PrimeReport is what a burst achieved. Sent counts GETs that ended with an
// HTTP response or a transport error; OK counts 2xx responses.
type PrimeReport struct {
	Sent     int
	OK       int
	Duration time.Duration
}

// ErrPrimeInvalidPath refuses a path that is not an absolute URL path.
var ErrPrimeInvalidPath = errors.New("prime path must start with /")

// Prime sends opts.Requests GETs to the live instance's HTTP port, one at a
// time, until the budget ends. The instance is held as an operation flight,
// so a quiet pause cannot pause the guest under the burst.
func (m *Manager) Prime(ctx context.Context, instance string, opts PrimeOptions) (PrimeReport, error) {
	opts, err := clampPrimeOptions(opts)
	if err != nil {
		return PrimeReport{}, fmt.Errorf("prime %s: %w", instance, err)
	}
	operationCtx, inst, flight, err := m.registerLiveInstanceFlight(ctx, instance)
	if err != nil {
		return PrimeReport{}, fmt.Errorf("prime %s: %w", instance, err)
	}
	defer m.finishInstanceFlight(instance, flight)
	if err := m.QuietResume(operationCtx, instance); err != nil {
		return PrimeReport{}, fmt.Errorf("prime %s: %w", instance, err)
	}
	addr := net.JoinHostPort(inst.Lease.HostIP.String(), "8080")
	return primeHTTP(withProbeHost(operationCtx, inst.ProbeHost), addr, opts), nil
}

func clampPrimeOptions(opts PrimeOptions) (PrimeOptions, error) {
	if !strings.HasPrefix(opts.Path, "/") {
		return opts, ErrPrimeInvalidPath
	}
	if opts.Requests <= 0 || opts.Requests > PrimeMaxRequests {
		opts.Requests = PrimeMaxRequests
	}
	if opts.Budget <= 0 || opts.Budget > PrimeMaxBudget {
		opts.Budget = PrimeMaxBudget
	}
	return opts, nil
}

// primeHTTP runs one burst against addr (host:port). Its transport is private
// and closed at the end, so no host-side keep-alive connection outlives the
// burst into the snapshot.
func primeHTTP(ctx context.Context, addr string, opts PrimeOptions) PrimeReport {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, opts.Budget)
	defer cancel()
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: primeRequestTimeout}).DialContext,
		MaxIdleConnsPerHost: 1,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   primeRequestTimeout,
		// A redirect may point off the guest; priming only calls the app.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var rep PrimeReport
	for i := 0; i < opts.Requests && ctx.Err() == nil; i++ {
		status, _, err := healthcheckProbeResult(ctx, client, addr, opts.Path)
		if err != nil || ctx.Err() != nil {
			break
		}
		rep.Sent++
		if status/100 == 2 {
			rep.OK++
		}
	}
	rep.Duration = time.Since(started)
	return rep
}
