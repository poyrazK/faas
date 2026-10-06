//go:build linux

package fcvm

// adr: 640
import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeMachineSocketChecksActualPeerCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "api.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	// A listener in this test process supplies real Linux credentials. This
	// observes a Unix peer; it does not establish native Firecracker acceptance.
	owner := nativeLaunchRecord{PID: os.Getpid(), Lease: Lease{UID: os.Getuid(), GID: os.Getgid()}}
	for _, scenario := range []string{"matches", "pid", "uid", "gid"} {
		t.Run(scenario, func(t *testing.T) {
			candidate := owner
			switch scenario {
			case "pid":
				candidate.PID++
			case "uid":
				candidate.Lease.UID++
			case "gid":
				candidate.Lease.GID++
			}
			conn, err := dialNativeMachine(context.Background(), path, candidate)
			if conn != nil {
				defer conn.Close()
			}
			if (err == nil) != (scenario == "matches") || scenario != "matches" && conn != nil {
				t.Fatalf("peer check conn=%v err=%v", conn, err)
			}
		})
	}
}
