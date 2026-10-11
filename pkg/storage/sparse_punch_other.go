//go:build !linux

package storage

import "os"

func punchHoles(*os.File, []fileRange) {}
