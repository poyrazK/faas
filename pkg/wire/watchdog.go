package wire

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// runtimeLoop is the loop every daemon gets for free: a goroutine
// that beats once a second. It only detects a fully wedged process
// (scheduler starvation, a stop-the-world that never ends), which is
// the floor of what the watchdog protects. Daemons with a real
// single-goroutine main loop register that loop too — schedd's
// notify/tick loop, vmmd's sweep — so a stall in the loop that owns
// the work is what trips the watchdog, not just a dead runtime.
const (
	runtimeLoop       = "runtime"
	runtimeLoopBudget = 30 * time.Second
	livenessSample    = time.Second
)

// StartWatchdog wires a daemon's Liveness into the systemd watchdog
// and the loop-liveness gauges. It:
//
//   - registers the "runtime" loop and beats it every second;
//   - samples every registered loop every second onto
//     ops.SetLoopLiveness (nil ops = metrics off);
//   - starts daemonunit.WatchdogFromEnv with liveness.Healthy as the
//     ping gate, so WATCHDOG=1 stops the moment any loop is past its
//     budget and systemd restarts the unit after WatchdogSec.
//
// Call it next to daemonunit.NotifyReadyWhen. The returned stop func
// halts the helpers; cancelling ctx does the same.
func StartWatchdog(ctx context.Context, liveness *Liveness, ops *OpsMetrics, log *slog.Logger) func() {
	if liveness == nil {
		return func() {}
	}
	liveness.Register(runtimeLoop, runtimeLoopBudget)
	sampleCtx, cancel := context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(livenessSample)
		defer t.Stop()
		var lastStalled string
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-t.C:
				liveness.Beat(runtimeLoop)
				stalled := ""
				for _, a := range liveness.Ages() {
					ops.SetLoopLiveness(a.Loop, a.Age, a.Stalled)
					if a.Stalled {
						stalled += a.Loop + " "
					}
				}
				if stalled != lastStalled && log != nil {
					if stalled != "" {
						log.Error("wire: daemon loop stalled; watchdog pings suspended", "loops", stalled)
					} else {
						log.Info("wire: daemon loops recovered; watchdog pings resumed")
					}
				}
				lastStalled = stalled
			}
		}
	}()
	stopWD := daemonunit.WatchdogFromEnv(sampleCtx, liveness.Healthy)
	return func() {
		stopWD()
		cancel()
	}
}
