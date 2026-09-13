package fcvm

import (
	"os/exec"
	"testing"
)

func TestLifecycleChildDoesNotInheritSystemdNotifyEnvironment(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "/run/systemd/notify")
	t.Setenv("WATCHDOG_PID", "123")
	t.Setenv("WATCHDOG_USEC", "1000000")
	cmd := exec.Command("sh", "-c", `test -z "${NOTIFY_SOCKET+x}" && test -z "${WATCHDOG_PID+x}" && test -z "${WATCHDOG_USEC+x}"`)
	isolateLifecycleChild(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated lifecycle child inherited systemd notification environment: %v: %s", err, output)
	}
}
