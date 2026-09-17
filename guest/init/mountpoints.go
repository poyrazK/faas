package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensureMountpoints creates the directories the post-pivot mounts need inside
// the new root. A customer image is not obliged to ship them: scratch-based
// and other minimal images have no /dev, /proc or /sys at all, and mounting
// devtmpfs onto a path that does not exist fails with ENOENT.
//
// That failure used to be a warning. The guest then had no /dev/null, the app
// supervisor failed on its first exec —
//
//	guest-init: app restart (restart 1/3 policy=on-failure): run
//	[/bin/sh -c cat app/hello.txt]: open /dev/null: no such file or directory
//
// — crash-looped three times, and init exited into a kernel panic, all inside
// two seconds and visible only on the serial console. On the host it read as
// "guest not ready after 30s: no readiness connection was accepted"
// (e2e-native smoke run 35163396260, every image-deploy test).
func ensureMountpoints(root string, dirs ...string) error {
	for _, d := range dirs {
		p := filepath.Join(root, d)
		info, err := os.Lstat(p)
		switch {
		case err == nil && info.IsDir():
			continue
		case err == nil:
			return fmt.Errorf("mountpoint %s exists in the image but is not a directory (%s)", d, info.Mode().Type())
		case !os.IsNotExist(err):
			return fmt.Errorf("mountpoint %s: %w", d, err)
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return fmt.Errorf("create mountpoint %s: %w", d, err)
		}
	}
	return nil
}
