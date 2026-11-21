package main

import (
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
// A unix listener (single box, no TLS) is never wrapped.
func wrapForwardHop(lis net.Listener, serverTLS *tls.Config, impl *vmmdgrpc.Server, getenv func(string) string, reg prometheus.Registerer, log *slog.Logger) net.Listener {
	if strings.TrimSpace(getenv(forwardHopEnv)) != "1" {
		return lis
	}
	if impl == nil {
		log.Warn("vmmd: forward hop has no service; staying on gRPC only")
		return lis
	}
	if serverTLS == nil || lis.Addr().Network() != "tcp" {
		log.Warn("vmmd: forward hop needs the TCP mTLS listener; staying on gRPC only", "listener", lis.Addr().String())
		return lis
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
	srv, err := vmmdhop.NewServer(vmmdhop.ServerConfig{
		TLS:         serverTLS,
		Service:     impl,
		Interceptor: vmmdgrpc.CallerPolicyStreamInterceptor(),
		Streams:     vmmdhop.StreamHandlers(&vmmdpb.Vmmd_ServiceDesc, "ForwardHTTPStream"),
		Log:         log,
		Conns:       func(result string) { conns.WithLabelValues(result).Inc() },
	})
	if err != nil {
		log.Warn("vmmd: forward hop not started; staying on gRPC only", "err", err)
		return lis
	}
	log.Info("vmmd: forward hop on", "alpn", vmmdhop.ALPN, "listener", lis.Addr().String())
	return vmmdhop.NewListener(lis, srv)
}
