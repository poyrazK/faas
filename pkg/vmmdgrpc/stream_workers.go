package vmmdgrpc

import (
	"runtime"

	"google.golang.org/grpc"
)

// StreamWorkers runs stream handlers on reused goroutines, one per P. A
// forward's handler grows its stack through admission and the bridge request,
// and a new goroutine per stream paid that growth on every request: stack
// growth was 7-9% of vmmd's CPU at 300 req/s (2026-10-31 profile). A stream
// that finds every worker busy, such as a long-lived raw or SSE stream,
// still gets a goroutine of its own, so no stream waits for a worker.
func StreamWorkers() grpc.ServerOption {
	return grpc.NumStreamWorkers(uint32(runtime.GOMAXPROCS(0)))
}
