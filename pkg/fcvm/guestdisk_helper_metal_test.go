//go:build metal

package fcvm

import "os"

// Metal tests cold-boot through the Manager, which edits guest drives only
// in the vmmd-guestdisk sandbox and looks for that helper beside its own
// executable (ADR-20261025090100). The test binary is the helper: TestMain
// dispatches the sandbox's --uid invocation to guestdisk.SandboxMain, as
// cmd/vmmd-guestdisk does, so point the Manager at it.
func init() {
	if exe, err := os.Executable(); err == nil {
		guestDiskHelperOverride = exe
	}
}
