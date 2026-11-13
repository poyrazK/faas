//go:build metal

package fcvm

import "os"

// Metal tests cold-boot through the Manager, which edits guest drives only
// in the vmmd-guestdisk sandbox (ADR-20261025090100). make test-metal builds
// the real helper beside FAAS_TEST_VMMD_BINARY; use it, as production does.
// The test binary is only the fallback (TestMain dispatches the sandbox's
// --uid invocation to guestdisk.SandboxMain): it is built with -race, whose
// shadow mappings already exceed the tool's address-space cap, so any
// allocation the helper makes once the cap is set kills it with "fatal
// error: runtime: cannot allocate memory" before the tool starts.
func init() {
	if vmmd := os.Getenv("FAAS_TEST_VMMD_BINARY"); vmmd != "" {
		if helper, err := resolveGuestDiskHelper(vmmd); err == nil {
			guestDiskHelperOverride = helper
			return
		}
	}
	if exe, err := os.Executable(); err == nil {
		guestDiskHelperOverride = exe
	}
}
