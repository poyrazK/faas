// Command agentproxyd is the agent egress gateway's politeness proxy
// (ADR-20261117185346 §4). It runs on the agent gateway host: nftables
// redirects agent TCP 80 and 443 arriving on wg-agent to its two listeners,
// and vmmd registers each agent VM's tunnel address on its registration
// listener, which binds only the gateway's tunnel address.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/onebox-faas/faas/pkg/agentproxy"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/origdst"
)

type config struct {
	tunnel             netip.Prefix
	nodePrefix         int
	addresses          []netip.Addr
	httpListen         string
	tlsListen          string
	registrationListen string
	metricsListen      string
	blocklist          string
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("agentproxyd", flag.ContinueOnError)
	tunnel := fs.String("tunnel-cidr", "", "the agent tunnel network, egress.agent_gateway.tunnel_cidr")
	nodePrefix := fs.Int("node-prefix", 22, "the node slice length, egress.agent_gateway.node_prefix")
	addresses := fs.String("addresses", "", "the agent public addresses, comma-separated")
	httpListen := fs.String("http-listen", "127.0.0.1:15080", "where nftables redirects agent TCP 80")
	tlsListen := fs.String("tls-listen", "127.0.0.1:15443", "where nftables redirects agent TCP 443")
	registration := fs.String("registration-listen", "", "the gateway's tunnel address and port for vmmd's registrations")
	metrics := fs.String("metrics-listen", "127.0.0.1:9473", "the loopback Prometheus listener")
	blocklist := fs.String("blocklist", "", "the blocklist file, one domain per line; reloaded on SIGHUP")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	cfg := config{nodePrefix: *nodePrefix, httpListen: *httpListen, tlsListen: *tlsListen, registrationListen: *registration,
		metricsListen: *metrics, blocklist: *blocklist}
	var err error
	if cfg.tunnel, err = netip.ParsePrefix(*tunnel); err != nil || !cfg.tunnel.Addr().Is4() {
		return config{}, fmt.Errorf("agentproxyd: -tunnel-cidr %q must be an IPv4 network", *tunnel)
	}
	if cfg.nodePrefix <= cfg.tunnel.Bits() || cfg.nodePrefix > 28 {
		return config{}, fmt.Errorf("agentproxyd: -node-prefix /%d does not fit in %s", cfg.nodePrefix, cfg.tunnel)
	}
	for _, raw := range strings.Split(*addresses, ",") {
		a, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || !a.Is4() {
			return config{}, fmt.Errorf("agentproxyd: -addresses entry %q is not an IPv4 address", raw)
		}
		cfg.addresses = append(cfg.addresses, a)
	}
	host, _, err := net.SplitHostPort(cfg.registrationListen)
	if a, perr := netip.ParseAddr(host); err != nil || perr != nil || !cfg.tunnel.Contains(a) {
		return config{}, fmt.Errorf("agentproxyd: -registration-listen %q must be an address in the agent tunnel", cfg.registrationListen)
	}
	return cfg, nil
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		log.Error("agentproxyd: config", "err", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg, log); err != nil {
		log.Error("agentproxyd: exit", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg config, log *slog.Logger) error {
	registry := agentproxy.NewRegistry(agentproxy.Slices{Tunnel: cfg.tunnel, NodePrefix: cfg.nodePrefix},
		time.Duration(api.AgentEgressRegistrationTTLSec)*time.Second, nil)
	blocklist := &agentproxy.Blocklist{}
	if err := loadBlocklist(blocklist, cfg.blocklist); err != nil {
		return err
	}
	reg := prometheus.NewRegistry()
	p := &agentproxy.Proxy{Registry: registry, Blocklist: blocklist, Addresses: cfg.addresses,
		OriginalDestination: origdst.TCP, Dial: dialFrom, PeekTimeout: time.Duration(api.AgentEgressPeekTimeoutSec) * time.Second,
		Log: log, Metrics: agentproxy.NewMetrics(reg, registry)}
	p.Configure(agentproxy.Caps{PerVM: api.AgentEgressConnsPerVM, PerAccountDomain: api.AgentEgressConnsPerAccountDomain,
		PerDomainFleet: api.AgentEgressConnsPerDomainFleet})

	errs := make(chan error, 4)
	for _, addr := range []string{cfg.httpListen, cfg.tlsListen} {
		ln, err := net.Listen("tcp4", addr)
		if err != nil {
			return fmt.Errorf("agentproxyd: listen %s: %w", addr, err)
		}
		go func() { errs <- p.Serve(ctx, ln) }()
	}
	servers := []*http.Server{
		{Addr: cfg.registrationListen, Handler: agentproxy.Handler(registry), ReadHeaderTimeout: 5 * time.Second},
		{Addr: cfg.metricsListen, Handler: promhttp.HandlerFor(reg, promhttp.HandlerOpts{}), ReadHeaderTimeout: 5 * time.Second},
	}
	for _, srv := range servers {
		go func() {
			if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				errs <- fmt.Errorf("agentproxyd: %s: %w", srv.Addr, err)
			}
		}()
	}
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	sweep := time.NewTicker(time.Minute)
	defer sweep.Stop()
	log.Info("agentproxyd: serving", "tunnel", cfg.tunnel.String(), "addresses", len(cfg.addresses), "blocklist", blocklist.Len())
	for {
		select {
		case <-ctx.Done():
			for _, srv := range servers {
				shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				_ = srv.Shutdown(shutdownCtx)
				cancel()
			}
			return nil
		case err := <-errs:
			if err != nil {
				return err
			}
		case <-hup:
			if err := loadBlocklist(blocklist, cfg.blocklist); err != nil {
				log.Error("agentproxyd: blocklist reload failed; the previous list stays", "err", err)
			} else {
				log.Info("agentproxyd: blocklist reloaded", "domains", blocklist.Len())
			}
		case <-sweep.C:
			registry.Sweep()
		}
	}
}

func loadBlocklist(b *agentproxy.Blocklist, path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path) //nolint:forbidigo // operator-owned config path from a flag, not a customer path
	if err != nil {
		return fmt.Errorf("agentproxyd: open blocklist: %w", err)
	}
	defer func() { _ = f.Close() }()
	return b.Load(f)
}

// dialFrom connects to dst from the agent address local.
func dialFrom(ctx context.Context, local netip.Addr, dst netip.AddrPort) (net.Conn, error) {
	d := net.Dialer{LocalAddr: net.TCPAddrFromAddrPort(netip.AddrPortFrom(local, 0))}
	return d.DialContext(ctx, "tcp4", dst.String())
}
