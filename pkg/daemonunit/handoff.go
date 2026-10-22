package daemonunit

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// HandoffReadyFDEnv names the pipe a hand-off successor reports readiness on
// (docs/adr/1736-zero-downtime-daemon-restarts.md). pkg/wire starts the
// successor; this package signals because readiness is reported here.
const HandoffReadyFDEnv = "FAAS_HANDOFF_READY_FD"

// HandoffReadyMessage is the line a ready successor writes.
const HandoffReadyMessage = "ready"

// Pre-traffic checks (docs/adr/20261020214632-automatic-unit-deploy-safety-
// rails.md): a predecessor that checks its successor passes a pipe in
// HandoffGoFDEnv. Once ready, the successor writes HandoffChecksMessage
// instead of HandoffReadyMessage and keeps its shared listeners closed to
// traffic; pkg/wire serves the predecessor's check connections meanwhile.
// When every check passed the predecessor writes HandoffGoMessage, and the
// successor opens its listeners and reports HandoffReadyMessage.
const (
	HandoffGoFDEnv       = "FAAS_HANDOFF_GO_FD"
	HandoffChecksMessage = "checks"
	HandoffGoMessage     = "go"
)

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
		pipe := inheritedPipe(HandoffReadyFDEnv, "handoff-ready")
		goPipe := inheritedPipe(HandoffGoFDEnv, "handoff-go")
		if pipe == nil || goPipe == nil {
			close(handoffReady)
			if pipe != nil {
				_, _ = pipe.WriteString(HandoffReadyMessage + "\n")
				_ = pipe.Close()
			}
			return
		}
		// Checked successor: serve only the predecessor's checks until it
		// says go. Without go (the checks failed, or the predecessor died)
		// the listeners stay closed and the predecessor stops this process.
		_, _ = pipe.WriteString(HandoffChecksMessage + "\n")
		go func() {
			line, _ := bufio.NewReader(goPipe).ReadString('\n')
			_ = goPipe.Close()
			if strings.TrimSpace(line) != HandoffGoMessage {
				return
			}
			close(handoffReady)
			_, _ = pipe.WriteString(HandoffReadyMessage + "\n")
			_ = pipe.Close()
		}()
	})
}

// inheritedPipe opens the descriptor named by env, or returns nil.
func inheritedPipe(env, name string) *os.File {
	fd, err := strconv.Atoi(os.Getenv(env))
	if err != nil || fd < 3 {
		return nil
	}
	return os.NewFile(uintptr(fd), name)
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
