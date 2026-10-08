// adr: 568 — environment intent and runtime ownership contracts.
// adr: 771
package fcvm

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/jailsetup"
)

// The test binary hosts the gate fixture before testing parses the production
// helper's command arguments. It never executes a real jailer or guest.
func runNativeLaunchHelperFixture() {
	if os.Getenv("GREGALE_NATIVE_LAUNCH_FIXTURE") != "1" {
		return
	}
	if os.Getenv("GREGALE_NATIVE_REAL_SESSION_POLICY") == "1" {
		if !jailsetup.Run(os.Args) {
			os.Exit(10)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && (os.Args[1] == jailsetup.SessionHostCommandMode || os.Args[1] == jailsetup.SessionJailDeviceMode) {
		fd := uintptr(4)
		if os.Args[1] == jailsetup.SessionJailDeviceMode {
			fd = 8
		}
		ready := os.NewFile(fd, "fixture-session-policy")
		_, _ = ready.Write([]byte{jailsetup.HostHelperSessionPolicyVersion})
		_ = ready.Close()
	}
	gate := os.NewFile(3, "fixture-launch-gate")
	if err := jailsetup.AwaitLaunchGate(gate); err != nil {
		if outcome := os.Getenv("GREGALE_NATIVE_GATE_OUTCOME"); outcome != "" {
			if err := publishNativeFixtureOutcome(outcome, []byte("rejected")); err != nil {
				os.Exit(9)
			}
		}
		os.Exit(2)
	}
	_ = gate.Close()
	if len(os.Args) == 4 && os.Args[1] == jailsetup.SessionJailDeviceMode {
		var scope jailsetup.DeviceSetupScope
		if json.Unmarshal([]byte(os.Args[3]), &scope) != nil || scope.Validate() != nil {
			os.Exit(5)
		}
		for i, identity := range scope.Identities() {
			file := os.NewFile(uintptr(4+i), "fixture-device-input")
			info, err := file.Stat()
			if err != nil {
				os.Exit(6)
			}
			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok || (jailsetup.DeviceFDIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}) != identity {
				os.Exit(7)
			}
		}
		receipt := jailsetup.DeviceSetupReceipt{Scope: scope, DevMountID: 123, TunMountID: 124, KVM: jailsetup.DeviceFDIdentity{Device: 31, Inode: 53}, KVMAPI: 12, TunAccessible: true}
		if os.Getenv("GREGALE_NATIVE_JAIL_DEVICE_BAD_RECEIPT") == "1" {
			receipt.Scope.StartTime++
		}
		if json.NewEncoder(os.Stdout).Encode(receipt) != nil {
			os.Exit(8)
		}
	}
	if marker := os.Getenv("GREGALE_NATIVE_HELPER_INPUT_MARKER"); marker != "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil || os.WriteFile(marker, data, 0o600) != nil {
			os.Exit(4)
		}
	}
	if output := os.Getenv("GREGALE_NATIVE_HELPER_OUTPUT"); output != "" {
		_, _ = os.Stdout.WriteString(output)
	}
	if err := os.WriteFile(os.Getenv("GREGALE_NATIVE_LAUNCH_MARKER"), []byte("authorized"), 0o600); err != nil {
		os.Exit(3)
	}
	if os.Getenv("GREGALE_NATIVE_HOLD_HELPER_FIXTURE") == "1" {
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

// A pathname is the crash fixture's completion signal. Readers must not see
// the empty file between creation and its outcome write.
func publishNativeFixtureOutcome(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".gate-outcome-")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
