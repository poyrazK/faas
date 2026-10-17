package e2etest

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// ReadyTimeoutScaleEnv multiplies every daemon readiness wait (socket
// listen, builderd and imaged subscriptions). The defaults suit CI-sized
// hosts. A 4-vCPU acceptance host restaging the builder base for a new
// guest-init, grype scan included, needs over 3 minutes before imaged
// subscribes, and builderd and schedd miss their 10 s and 30 s waits under
// disk pressure.
const ReadyTimeoutScaleEnv = "FAAS_E2E_READY_TIMEOUT_SCALE"

const maxReadyTimeoutScale = 20

// readyTimeout scales d by ReadyTimeoutScaleEnv: a whole number from 1 to
// 20. Unset or invalid keeps d.
func readyTimeout(d time.Duration) time.Duration {
	scale, err := strconv.Atoi(strings.TrimSpace(os.Getenv(ReadyTimeoutScaleEnv)))
	if err != nil || scale < 1 || scale > maxReadyTimeoutScale {
		return d
	}
	return d * time.Duration(scale)
}
