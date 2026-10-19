package migrationcutover

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

// EdgeAddressPolicy narrows public dialing to the operator's published edge.
// The smoke challenge is sent only to a trusted Gregale endpoint, never to an
// arbitrary public origin returned by a customer-controlled DNS answer.
func EdgeAddressPolicy(ctx context.Context, target string, configured []netip.Addr, lookup func(context.Context, string) ([]net.IPAddr, error), public func(net.IP) bool) (func(net.IP) bool, error) {
	trusted := map[string]bool{}
	if len(configured) > 0 {
		for _, address := range configured {
			ip := net.IP(address.AsSlice())
			if !public(ip) {
				return nil, errors.New("published edge address is not public")
			}
			trusted[ip.String()] = true
		}
	} else {
		if target == "" {
			return nil, errors.New("published edge target is unavailable")
		}
		addresses, err := lookup(ctx, target)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("published edge target could not be resolved")
		}
		for _, address := range addresses {
			if address.Zone != "" || !public(address.IP) {
				return nil, errors.New("published edge target is not public")
			}
			trusted[address.IP.String()] = true
		}
	}
	return func(ip net.IP) bool { return public(ip) && trusted[ip.String()] }, nil
}

// PublicClient resolves once per connection, rejects the entire mixed answer
// set if any address is forbidden, and dials only validated numeric addresses.
// TLS still verifies the hostname and system trust roots. Environment proxies
// and redirects cannot turn an owned domain into an internal-network probe.
func PublicClient(host string, lookup func(context.Context, string) ([]net.IPAddr, error), allowed func(net.IP) bool, dial func(context.Context, string, string) (net.Conn, error)) *http.Client {
	transport := &http.Transport{
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		DisableKeepAlives:     true,
		DisableCompression:    true,
		TLSHandshakeTimeout:   api.MigrationCutoverProbeTimeout,
		ResponseHeaderTimeout: api.MigrationCutoverProbeTimeout,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			name, port, err := net.SplitHostPort(address)
			if err != nil || name != host || port != "443" {
				return nil, errors.New("cutover endpoint is not allowed")
			}
			addresses, err := lookup(ctx, host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("cutover hostname could not be resolved")
			}
			for _, ip := range addresses {
				if ip.Zone != "" || !allowed(ip.IP) {
					return nil, errors.New("cutover hostname must resolve only to public addresses")
				}
			}
			for _, ip := range addresses {
				conn, err := dial(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if err == nil {
					return conn, nil
				}
			}
			return nil, errors.New("cutover endpoint could not be reached")
		},
	}
	return &http.Client{Transport: transport, Timeout: api.MigrationCutoverProbeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func ProbeRoute(ctx context.Context, plan api.MigrationCutover, client *http.Client, authorize func(context.Context, string, string, time.Time) error) []api.MigrationVerificationCheck {
	var tlsVerified atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{TLSHandshakeDone: func(connection tls.ConnectionState, err error) {
		if err == nil && len(connection.VerifiedChains) > 0 {
			tlsVerified.Store(true)
		}
	}})
	v := apihostingreceipt.Verifier{Client: client, BaseURL: "https://" + plan.Request.Hostname, Required: true, Timeout: api.MigrationCutoverProbeTimeout, RequestTimeout: api.MigrationCutoverProbeTimeout, RetryInterval: time.Second, Authorize: authorize}
	result, _ := v.VerifyDeployment(ctx, plan.App, plan.SmokePath, plan.DeploymentID)
	tlsCheck := api.MigrationVerificationCheck{Name: "tls", Status: "pending", Detail: "Waiting for a trusted HTTPS certificate for this hostname. Check DNS, CAA and any CDN proxy setting."}
	if tlsVerified.Load() {
		tlsCheck.Status, tlsCheck.Detail = "passed", "A trusted HTTPS connection verified this hostname."
	}
	route := api.MigrationVerificationCheck{Name: "deployment_route", Status: "pending", Detail: "Waiting for a successful response with Gregale gateway proof for the selected deployment. A response from the previous provider does not pass."}
	if result.Status == apihostingreceipt.SmokeVerified {
		route.Status, route.Detail = "passed", "This production hostname returned 2xx with gateway proof for the selected deployment."
	}
	return []api.MigrationVerificationCheck{tlsCheck, route}
}
