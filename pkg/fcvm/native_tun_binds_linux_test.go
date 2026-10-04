//go:build linux

// adr: 532 — TUN mounts require device-capable access and exact mount identity.
package fcvm

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeTunPinnedSourceRejectsRegularFilesAndOtherDevices(t *testing.T) {
	for _, path := range []string{os.Args[0], "/dev/null"} {
		file, err := os.OpenFile(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = nativeTunFileSource(file)
		if closeErr := file.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err == nil {
			t.Fatalf("non-TUN source accepted: %s", path)
		}
	}
}
