package main

import (
	"log/slog"
	"os"
	"sync"
	"syscall"
)

// workloadStopControl hands a host-delivered stop signal to the running
// workload's signal loop. A long-running guest's liveness listener owns vsock
// port 1028, the job-control port, so the host's stop frame
// (VsockJobCancelMsgType) arrives there and is dispatched here; a job guest
// runs no liveness listener and serves the port itself (Hunt 14 H14-59).
var workloadStopControl struct {
	mu      sync.Mutex
	signals chan<- os.Signal
}

// registerWorkloadStopSignals routes host-delivered stop signals into
// signals until the returned function is called.
func registerWorkloadStopSignals(signals chan<- os.Signal) func() {
	workloadStopControl.mu.Lock()
	workloadStopControl.signals = signals
	workloadStopControl.mu.Unlock()
	return func() {
		workloadStopControl.mu.Lock()
		if workloadStopControl.signals == signals {
			workloadStopControl.signals = nil
		}
		workloadStopControl.mu.Unlock()
	}
}

// deliverWorkloadStopSignal queues sig for the registered signal loop. It
// refuses a signal outside the workload stop set, no registered loop, or a
// full queue.
func deliverWorkloadStopSignal(sig syscall.Signal) bool {
	if !isWorkloadStopSignal(sig) {
		return false
	}
	workloadStopControl.mu.Lock()
	signals := workloadStopControl.signals
	workloadStopControl.mu.Unlock()
	if signals == nil {
		return false
	}
	select {
	case signals <- sig:
		return true
	default:
		return false
	}
}

// handleWorkloadStopConn answers a stop frame that reached the liveness
// listener with the job-control ack byte the host expects, then closes.
func handleWorkloadStopConn(f *os.File, sig uint32, log *slog.Logger) {
	ack := VsockJobControlAckError
	if deliverWorkloadStopSignal(syscall.Signal(sig)) {
		ack = VsockJobControlAckOK
		log.Info("guest-init: host stop signal received", "signal", syscall.Signal(sig).String())
	}
	_, _ = f.Write([]byte{ack})
}
