package fcvm

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// A guest controls its disk's superblock, so every host mount of a
// guest-written image pins ext4 and overrides the error behaviour: a guest
// that set `tune2fs -e panic` and corrupted metadata must not panic the host.
func TestGuestDiskMountArgsPinExt4AndErrorBehaviour(t *testing.T) {
	for _, mode := range []string{"rw", "ro", "ro,noload"} {
		args := guestDiskMountArgs(mode, "/srv/fc/jail/i/drive1.ext4", "/tmp/mp")
		if len(args) != 6 || args[0] != "-t" || args[1] != "ext4" || args[2] != "-o" || args[4] != "/srv/fc/jail/i/drive1.ext4" || args[5] != "/tmp/mp" {
			t.Fatalf("%s: argv = %q", mode, args)
		}
		options := strings.Split(args[3], ",")
		for _, want := range append([]string{"loop", "nodev", "nosuid", "noexec", "errors=remount-ro"}, strings.Split(mode, ",")...) {
			if !slices.Contains(options, want) {
				t.Errorf("%s: options %q lack %q", mode, args[3], want)
			}
		}
	}
}

// Every loop mount in the package goes through guestDiskMountArgs, so a new
// call site cannot drop the hardening.
func TestNoLoopMountBypassesGuestDiskMountArgs(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	raw := regexp.MustCompile(`exec\.Command(Context)?\([^)]*"mount",\s*"-o",\s*"loop`)
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if raw.Match(body) {
			t.Errorf("%s loop-mounts an image without guestDiskMountArgs", name)
		}
		if strings.Contains(string(body), `unix.MS_NOEXEC, "")`) {
			t.Errorf("%s mounts ext4 without an errors= behaviour override", name)
		}
	}
}
