package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/vmmdhop"
)

// forwardHopEnv turns on the light forward hop on vmmd's gRPC port
// (ADR-20261121202814). Off by default: with it off the listener is
// untouched.
const forwardHopEnv = "FAAS_VMMD_FORWARD_HOP"

// wrapForwardHop puts the hop's ALPN router in front of lis when the switch
// is on. The hop serves only ForwardHTTPStream, through the same caller
// policy interceptor as the gRPC server, on impl, with vmmd's server TLS.
// A unix listener (single box, no TLS) is never wrapped. The returned server
// is nil when the hop is off; stop it with the gRPC server (hopStopper).
func wrapForwardHop(ctx context.Context, lis net.Listener, serverTLS *tls.Config, impl *vmmdgrpc.Server, getenv func(string) string, reg prometheus.Registerer, log *slog.Logger) (net.Listener, *vmmdhop.Server) {
	if strings.TrimSpace(getenv(forwardHopEnv)) != "1" {
		return lis, nil
	}
	if impl == nil {
		log.Warn("vmmd: forward hop has no service; staying on gRPC only")
		return lis, nil
	}
	if serverTLS == nil || lis.Addr().Network() != "tcp" {
		log.Warn("vmmd: forward hop needs the TCP mTLS listener; staying on gRPC only", "listener", lis.Addr().String())
		return lis, nil
	}
	conns := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vmmd_forward_hop_conns_total",
		Help: "Connections that offered the forward hop (ADR-20261121202814), by result: served, handshake_error, refused.",
	}, []string{"result"})
	if err := reg.Register(conns); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			log.Warn("vmmd: forward hop metric not registered", "err", err)
		} else if existing, ok := already.ExistingCollector.(*prometheus.CounterVec); ok {
			conns = existing
		}
	}
	srv, err := vmmdhop.NewServer(ctx, vmmdhop.ServerConfig{
		TLS:         serverTLS,
		Service:     impl,
		Interceptor: vmmdgrpc.CallerPolicyStreamInterceptor(),
		Streams:     vmmdhop.StreamHandlers(&vmmdpb.Vmmd_ServiceDesc, "ForwardHTTPStream"),
		Log:         log,
		Conns:       func(result string) { conns.WithLabelValues(result).Inc() },
	})
	if err != nil {
		log.Warn("vmmd: forward hop not started; staying on gRPC only", "err", err)
		return lis, nil
	}
	log.Info("vmmd: forward hop on", "alpn", vmmdhop.ALPN, "listener", lis.Addr().String())
	return vmmdhop.NewListener(ctx, lis, srv), srv
}

// hopStopper stops the gRPC server and the forward hop together, so
// shutdown treats hop exchanges as it treats gRPC streams: GracefulStop
// waits for both, Stop ends both. impl.Close runs only after either.
type hopStopper struct {
	grpc grpcStopper
	hop  *vmmdhop.Server
}

func (h hopStopper) GracefulStop() {
	h.grpc.GracefulStop()
	if h.hop != nil {
		h.hop.Drain()
		h.hop.Wait()
	}
}

func (h hopStopper) Stop() {
	h.grpc.Stop()
	if h.hop != nil {
		h.hop.Close()
	}
}
