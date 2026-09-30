package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/overlay"
	"github.com/onebox-faas/faas/pkg/scheddgrpc"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/udpd"
	"github.com/onebox-faas/faas/pkg/wire"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const defaultUDPDScheddTarget = "unix:///run/faas/schedd.sock"

// UDP requires explicit opt-in and source CIDRs. Shutdown cancels all peer
// sessions before dependencies close; datagrams have no half-close drain.
func startUDPIngress(ctx context.Context, log *slog.Logger, store *state.PgStore, metrics *udpd.Metrics) (stop func(), err error) {
	if !envBoolOr("FAAS_UDPD_ENABLED", false) {
		return func() {}, nil
	}
	if store == nil {
		return nil, errors.New("gatewayd-public: udpd requires a state store")
	}

	sources, err := udpSourcePrefixes(os.Getenv("FAAS_UDPD_ALLOWED_SOURCE_CIDRS"))
	if err != nil {
		return nil, err
	}
	vmmdTLS, err := wire.LoadClientTLSConfigWithPrefix(
		"udpd_vmmd_",
		os.Getenv("FAAS_UDPD_VMMD_TLS_CERT_PATH"),
		os.Getenv("FAAS_UDPD_VMMD_TLS_KEY_PATH"),
		os.Getenv("FAAS_UDPD_VMMD_TLS_CA_PATH"),
	)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: load udpd vmmd TLS: %w", err)
	}
	scheddTLS, err := wire.LoadClientTLSConfigWithPrefix(
		"udpd_schedd_",
		os.Getenv("FAAS_UDPD_SCHEDD_TLS_CERT_PATH"),
		os.Getenv("FAAS_UDPD_SCHEDD_TLS_KEY_PATH"),
		os.Getenv("FAAS_UDPD_SCHEDD_TLS_CA_PATH"),
	)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: load udpd schedd TLS: %w", err)
	}
	sched, err := scheddgrpc.DialContext(ctx, envOr("FAAS_UDPD_SCHEDD_TARGET", defaultUDPDScheddTarget), scheddTLS)
	if err != nil {
		return nil, fmt.Errorf("gatewayd-public: dial udpd schedd: %w", err)
	}

	// Reuse the same placement identity and vmmd transport as the HTTP gateway.
	// The cache is local to this process and closes only after udpd has drained.
	gateway.SetNodeResolver(func(resolveCtx context.Context, nodeID string) (string, bool) {
		node, resolveErr := store.ComputeNodeByID(resolveCtx, nodeID)
		if resolveErr != nil {
			if !errors.Is(resolveErr, state.ErrNotFound) {
				log.Warn("udpd: resolve compute node", "node_id", nodeID, "err", resolveErr)
			}
			return "", false
		}
		if !node.Active && node.Lifecycle != state.NodeLifecycleDraining {
			return "", false
		}
		return node.TargetURL, node.TargetURL != ""
	})
	nodes := gateway.NewNodeClientCache(func(dialCtx context.Context, target string) (*grpc.ClientConn, error) {
		return overlay.Dial(dialCtx, overlay.New(target), vmmdTLS)
	}, log)
	closeDependencies := func() {
		_ = sched.Close()
		_ = nodes.Close()
	}

	resolver := &udpd.StoreTargetResolver{Store: store, Admitter: sched}
	ready := make(chan struct{})
	done := make(chan struct{})
	result := make(chan error, 1)
	supervisor := &udpd.Supervisor{
		BindHost: envOr("FAAS_UDPD_BIND_HOST", "0.0.0.0"), Source: store,
		AllowedSources: sources, ResolveTarget: resolver.ResolveTarget, Metrics: metrics,
		Forwarder: gateway.UDPForwarder{Nodes: nodes},
		OnReady:   func() { close(ready) },
		OnError: func(err error) {
			if status.Code(err) == codes.ResourceExhausted {
				log.Warn("gatewayd-public: UDP peer resource exhausted", "err", err)
				return
			}
			log.Error("gatewayd-public: udpd runtime error", "err", err)
		},
	}
	udpCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer close(done)
		serveErr := supervisor.Serve(udpCtx)
		result <- serveErr
		if serveErr != nil && udpCtx.Err() == nil {
			log.Error("gatewayd-public: udpd stopped", "err", serveErr)
		}
	}()
	select {
	case <-ready:
	case serveErr := <-result:
		cancel()
		<-done
		closeDependencies()
		if serveErr == nil {
			serveErr = errors.New("udpd stopped before readiness")
		}
		return nil, fmt.Errorf("gatewayd-public: start udpd: %w", serveErr)
	case <-ctx.Done():
		cancel()
		<-done
		closeDependencies()
		return nil, ctx.Err()
	}
	var once sync.Once
	stop = func() { once.Do(func() { cancel(); <-done; closeDependencies() }) }
	log.Info("gatewayd-public: UDP ingress enabled", "bind_host", supervisor.BindHost, "allowed_sources", sources)
	return stop, nil
}

func udpSourcePrefixes(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("FAAS_UDPD_ALLOWED_SOURCE_CIDRS is required when UDP ingress is enabled")
	}
	var prefixes []netip.Prefix
	for _, token := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(token))
		if err != nil || !prefix.Addr().Is4() {
			return nil, fmt.Errorf("invalid IPv4 UDP source CIDR %q", token)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}
