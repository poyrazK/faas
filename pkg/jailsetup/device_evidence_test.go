// adr: 568 — namespace membership, pidfd target and mount flags gate device setup.
package jailsetup

import (
	"strings"
	"testing"
)

func TestDeviceFDMountIdentityRefusesAmbiguity(t *testing.T) {
	if id, valid := deviceFDMountID([]byte("pos:\t0\nmnt_id:\t123\n")); !valid || id != 123 {
		t.Fatal("original descriptor mount identity refused")
	}
	for _, data := range []string{"", "mnt_id:\t0\n", "mnt_id:\t-1\n", "mnt_id:\t123 extra\n", "mnt_id:\t123\nmnt_id:\t123\n", "mnt_id:\t123\nmnt_id:\t124\n"} {
		if _, valid := deviceFDMountID([]byte(data)); valid {
			t.Fatal("missing or ambiguous input mount identity accepted")
		}
	}
}

func TestDeviceNamespaceMembershipAndAccessProof(t *testing.T) {
	good := "123 1 0:7 / /jail/dev rw,nosuid,noexec - tmpfs tmpfs rw\n"
	if !deviceMountPresent([]byte(good), 123) || !deviceMountAccess([]byte(good), 123) {
		t.Fatal("complete original mount proof refused")
	}
	for _, data := range []string{good + "incomplete\n", strings.Replace(good, "rw,nosuid", "ro,nosuid", 1), strings.Replace(good, "rw,nosuid", "rw,nodev,nosuid", 1), strings.Replace(good, ",noexec", "", 1)} {
		if deviceMountPresent([]byte(data), 123) && deviceMountAccess([]byte(data), 123) {
			t.Fatalf("changed mount proof accepted: %s", data)
		}
	}
	if !devicePIDHandleMatches([]byte("Pid:\t222\nNSpid:\t222\n"), 222) {
		t.Fatal("correct pidfd target refused")
	}
	for _, data := range []string{"Pid:\t223\n", "Pid:\t-1\n", "Pid:\t222\nPid:\t222\n", "NSpid:\t222\n"} {
		if devicePIDHandleMatches([]byte(data), 222) {
			t.Fatal("ambiguous or recycled pidfd target accepted")
		}
	}
}
