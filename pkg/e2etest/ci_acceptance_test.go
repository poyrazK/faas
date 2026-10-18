package e2etest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCINativeHarnessRetainsOwnedFilesOnlyOnOptIn(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GREGALE_CI_ACCEPTANCE_EVIDENCE", root)
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "0")
	ordinary := harnessTempDir(t)
	if filepath.Dir(ordinary) == root {
		t.Fatal("disabled acceptance changed normal temporary directory ownership")
	}
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "1")
	retained := harnessTempDir(t)
	if filepath.Dir(retained) != root {
		t.Fatal("native CI recovery files were not retained with their journal")
	}
}

func TestCINativeHarnessDNSRequiresNativeGuest(t *testing.T) {
	t.Setenv("FAAS_TEST_KERNEL", "/fixture/kernel")
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "0")
	if nativeGuestDNS(APID | VMMD | Gatewayd) {
		t.Fatal("ordinary Jobs gained a host DNS listener")
	}
	if !nativeGuestDNS(APID | VMMD | Gatewayd | Builderd) {
		t.Fatal("builder native DNS changed")
	}
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "1")
	if !nativeGuestDNS(APID|VMMD|Gatewayd) || nativeGuestDNS(APID|Gatewayd) {
		t.Fatal("CI guest DNS did not remain scoped to the native VM harness")
	}
}
