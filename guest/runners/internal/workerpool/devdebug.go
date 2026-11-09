package workerpool

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// A `gregale dev --debug` function (ADR-741) runs its handler under the Node
// inspector that guest-init's preload opens in the first worker. Two pool
// behaviours would defeat it: requests spread over several workers, so most
// never reach the inspected process, and a worker that misses its deadline
// or cancel acknowledgement is killed, which is exactly what a breakpoint
// looks like. While debugging, the pool keeps one worker; while a debugger
// is attached, a paused worker is waited for instead of killed.

// devDebugging reports a developer environment started with the debugger.
// FAAS_DEVELOPER_ENVIRONMENT is platform identity, so the switch set on a
// production function has no effect here either.
var devDebugging = sync.OnceValue(func() bool {
	return os.Getenv(api.DevDebugEnv) == api.DevDebugRuntimeNode &&
		os.Getenv(api.PlatformDeveloperEnvironmentEnv) == "1"
})

// workerLimit is the pool's interpreter ceiling: one while debugging, so the
// inspected worker serves every request.
func workerLimit() int {
	if devDebugging() {
		return 1
	}
	return maxWorkers
}

// devDebugTCPTables are the kernel socket tables debuggerHolds reads.
var devDebugTCPTables = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// debuggerHolds reports whether a debugger is attached to the inspector, so a
// worker that stopped answering may be paused at a breakpoint rather than
// hung. With no debugger attached the pool keeps its normal deadlines.
func debuggerHolds() bool {
	if !devDebugging() {
		return false
	}
	for _, path := range devDebugTCPTables {
		if inspectorConnectionEstablished(path, api.DevDebugNodePort) {
			return true
		}
	}
	return false
}

// inspectorConnectionEstablished scans one /proc/net/tcp{,6} table for an
// ESTABLISHED (state 01) socket whose local port is port.
func inspectorConnectionEstablished(path string, port int) bool {
	f, err := os.Open(path) //nolint:forbidigo // a fixed /proc/net/tcp{,6} kernel table, not a customer path
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[3] != "01" {
			continue
		}
		_, portHex, ok := strings.Cut(fields[1], ":")
		if !ok {
			continue
		}
		if local, err := strconv.ParseUint(portHex, 16, 16); err == nil && int(local) == port {
			return true
		}
	}
	return false
}
