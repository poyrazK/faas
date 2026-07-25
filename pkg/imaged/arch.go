package imaged

import "runtime"

// BuilderArch returns the host architecture the imaged binary is compiled
// for. Every storage key the base / app layer / digest sidecar is
// published under is partitioned by this value (issue #197 B3.3) — the
// same runtime + slug produces different ext4s on amd64 vs arm64 (the
// initramfs, kernel modules, and userland binaries don't cross over).
//
// Returns runtime.GOARCH. The lifecycle is one-binary-one-arch: imaged
// only stages the base ext4 for the host it's running on, and a
// multi-arch fleet runs a separate imaged binary per arch.
func BuilderArch() string {
	return runtime.GOARCH
}
