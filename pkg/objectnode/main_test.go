package objectnode

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain runs this package's tests on Linux only. Object nodes are Linux
// storage hosts: the suite exercises renameat2, fsync ordering and SIGKILL
// recovery as Linux provides them, and on macOS it takes over ten minutes
// and its kill-recovery test fails. The package still builds everywhere, so
// packages that import it keep testing on developer Macs.
func TestMain(m *testing.M) {
	if runtime.GOOS != "linux" {
		fmt.Printf("objectnode: tests run on Linux only (this is %s)\n", runtime.GOOS)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
