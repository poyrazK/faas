package daemonunit

import (
	"os"
	"strconv"
	"sync"
)

// HandoffReadyFDEnv names the pipe a hand-off successor reports readiness on
// (docs/adr/1580-zero-downtime-daemon-restarts.md). pkg/wire starts the
// successor; this package signals because readiness is reported here.
const HandoffReadyFDEnv = "FAAS_HANDOFF_READY_FD"

// HandoffReadyMessage is the line a ready successor writes.
const HandoffReadyMessage = "ready"

var handoffReadyOnce sync.Once

// SignalHandoffReady tells the predecessor that started this process that it
// is serving, so it can make this process the unit's main process and stop
// accepting. It does nothing in a process not started by a hand-off.
func SignalHandoffReady() {
	handoffReadyOnce.Do(func() {
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
