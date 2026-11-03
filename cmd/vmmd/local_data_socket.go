package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/user"
	"strconv"

	"google.golang.org/grpc"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"github.com/onebox-faas/faas/pkg/wire"
)

// localDataSocket serves this node's gatewayd-internal on a unix socket next
// to vmmd's mTLS listener (ADR-20261030230157). A nil *localDataSocket is the
// switch turned off.
type localDataSocket struct {
	srv *grpc.Server
	lis net.Listener
}

// lookupGatewayUID resolves the one uid the local data socket admits.
var lookupGatewayUID = func() (int, error) {
	u, err := user.Lookup(api.GatewaydInternalUser)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(u.Uid)
}

// newLocalDataSocket binds the local data socket at path and registers vmmd's
// service on it with register; serve starts it. With the switch off it removes a socket an earlier run
// left, so the gateway goes back to mTLS. A socket that cannot be set up is
// removed and logged rather than failing vmmd: the gateway then keeps mTLS.
func newLocalDataSocket(ctx context.Context, enabled bool, path, ownerUser string, register func(*grpc.Server), log *slog.Logger) *localDataSocket {
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("vmmd: could not remove the local data socket", "path", path, "err", err)
		}
		return nil
	}
	s, err := listenLocalDataSocket(ctx, path, ownerUser, register)
	if err != nil {
		log.Warn("vmmd: local data socket unavailable; this node's gateway keeps mTLS", "path", path, "err", err)
		_ = os.Remove(path)
		return nil
	}
	return s
}

func listenLocalDataSocket(ctx context.Context, path, ownerUser string, register func(*grpc.Server)) (*localDataSocket, error) {
	uid, err := lookupGatewayUID()
	if err != nil {
		return nil, fmt.Errorf("look up %s: %w", api.GatewaydInternalUser, err)
	}
	lis, err := wire.ListenAs(ctx, "unix://"+path, nil, ownerUser)
	if err != nil {
		return nil, err
	}
	srv := grpc.NewServer(append(append(vmmdgrpc.LocalDataServerOptions(uid), wire.TraceServerOptions()...), vmmdgrpc.StreamWorkers())...)
	register(srv)
	return &localDataSocket{srv: srv, lis: lis}, nil
}

// serve accepts connections until stop.
func (s *localDataSocket) serve(log *slog.Logger) {
	if s == nil {
		return
	}
	log.Info("vmmd: local data socket serving", "addr", s.lis.Addr().String())
	go func() { _ = s.srv.Serve(s.lis) }()
}

// stop ends the socket's RPCs the way stopGRPC ends the main listener's, in
// the background so both listeners share one shutdown grace. The returned
// channel closes when it is done.
func (s *localDataSocket) stop(adopting bool, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if s == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		stopGRPC(s.srv, adopting, log)
	}()
	return done
}
