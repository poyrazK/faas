package wire

import (
	"os"

	"google.golang.org/grpc"
)

// LocalStaticWindowEnv turns on fixed HTTP/2 flow-control windows for the
// gatewayd-internal ↔ vmmd local data socket. Off by default.
//
// gRPC's default dynamic flow control sizes the window by probing the link's
// bandwidth-delay product: on received data it sends a WINDOW_UPDATE plus a
// PING and waits for the ACK. On a node-local unix socket that probe measures
// nothing useful, yet on production forwards it is about 40% of vmmd's writes
// to the gateway, each one waking the peer. Fixed windows turn the probe off.
// They only change flow control on a local socket; peer authentication,
// authorization and message limits are untouched.
const LocalStaticWindowEnv = "FAAS_VMMD_DATA_STATIC_WINDOW"

const (
	// localStreamWindow and localConnWindow bound what one side may send
	// before the other reads it. They are ceilings, not allocations, and sit
	// at or above what the probe grows the windows to on this socket, so large
	// bodies keep their throughput.
	localStreamWindow int32 = 4 << 20
	localConnWindow   int32 = 16 << 20
)

// LocalStaticWindowEnabled reports whether LocalStaticWindowEnv is "1".
func LocalStaticWindowEnabled() bool { return os.Getenv(LocalStaticWindowEnv) == "1" }

// LocalStaticWindowDialOptions returns the client options for fixed windows.
func LocalStaticWindowDialOptions() []grpc.DialOption {
	return []grpc.DialOption{
		grpc.WithStaticStreamWindowSize(localStreamWindow),
		grpc.WithStaticConnWindowSize(localConnWindow),
	}
}

// LocalStaticWindowServerOptions returns the server options for fixed windows.
func LocalStaticWindowServerOptions() []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.StaticStreamWindowSize(localStreamWindow),
		grpc.StaticConnWindowSize(localConnWindow),
	}
}
