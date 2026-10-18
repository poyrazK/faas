package daemonunit

import (
	"os"
	"strconv"
	"sync"
	"sync/atomic"
)

// HandoffReadyFDEnv names the pipe a hand-off successor reports readiness on
// (docs/adr/20261018072701-zero-downtime-daemon-restarts.md). pkg/wire starts the
// successor; this package signals because readiness is reported here.
const HandoffReadyFDEnv = "FAAS_HANDOFF_READY_FD"

// HandoffReadyMessage is the line a ready successor writes.
const HandoffReadyMessage = "ready"

var (
	handoffReadyOnce sync.Once
	handoffReady     = make(chan struct{})
	handedOff        atomic.Bool
)

// SignalHandoffReady tells the predecessor that started this process that it
// is serving, so it can make this process the unit's main process and stop
// accepting. In a process not started by a hand-off it only closes
// HandoffReady.
func SignalHandoffReady() {
	handoffReadyOnce.Do(func() {
		close(handoffReady)
		fd, err := strconv.Atoi(os.Getenv(HandoffReadyFDEnv))
		if err != nil || fd < 3 {
			return
		}
		pipe := os.NewFile(uintptr(fd), "handoff-ready")
		if pipe == nil {
			return
		}
		_, _ = pipe.WriteString(HandoffReadyMessage + "\n")
		_ = pipe.Close()
	})
}

// isHandoffSuccessor reports whether a predecessor waits for this process.
func isHandoffSuccessor() bool {
	return os.Getenv(HandoffReadyFDEnv) != ""
}

// HandoffReady is closed once this process has reported readiness. A
// hand-off successor accepts on the sockets it shares with its predecessor
// only after that, so the generation still serving takes every connection
// while the successor starts.
func HandoffReady() <-chan struct{} { return handoffReady }

// MarkHandedOff records that a successor is now the unit's main process.
// systemd accepts notifications only from the main process (NotifyAccess=main),
// so this process stops sending them: its watchdog pings and STOPPING=1 would
// only be rejected and logged.
func MarkHandedOff() { handedOff.Store(true) }
