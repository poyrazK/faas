//go:build darwin

// Stub for non-linux builds: there is no /proc/<pid>/fd to walk
// and no AF_VSOCK. probeListening's no-child early-out (in
// characterize_common.go) returns (0, "", false) for any pid,
// so this body is unreachable in tests but the linker requires
// the symbol to exist.
package main

func probeListeningLinux(pid, want int) (int, string, bool) {
	return 0, "", false
}
