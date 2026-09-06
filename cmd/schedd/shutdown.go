package main

import (
	"context"
	"google.golang.org/grpc"
)

// Capacity reports and warm hints are persistent RPCs. Bound their drain by
// the daemon shutdown budget so a healthy connected node cannot keep schedd
// alive until systemd forcibly kills it.
func stopGRPCServer(ctx context.Context, server *grpc.Server) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		server.Stop()
		<-done
	}
}
