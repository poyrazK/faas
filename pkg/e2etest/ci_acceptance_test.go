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

// ADR-1642: managed HTTPS probes and native CI share the guest DNS listener.
func TestCINativeHarnessDNSRequiresNativeGuest(t *testing.T) {
	t.Setenv("FAAS_TEST_KERNEL", "/fixture/kernel")
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "0")
	t.Setenv("FAAS_E2E_SERVICE_HTTPS_CA", "")
	if nativeGuestDNS(APID | VMMD | Gatewayd) {
		t.Fatal("ordinary Jobs gained a host DNS listener")
	}
	if !nativeGuestDNS(APID | VMMD | Gatewayd | Builderd) {
		t.Fatal("builder native DNS changed")
	}
	t.Setenv("FAAS_E2E_SERVICE_HTTPS_CA", "/fixture/service-ca.pem")
	if !nativeGuestDNS(APID|VMMD|Gatewayd) || nativeGuestDNS(APID|Gatewayd) {
		t.Fatal("managed HTTPS guest DNS did not remain scoped to the native VM harness")
	}
	t.Setenv("FAAS_TEST_KERNEL", "")
	if nativeGuestDNS(APID | VMMD | Gatewayd) {
		t.Fatal("managed HTTPS fixture gained host DNS without a native guest kernel")
	}
	t.Setenv("FAAS_TEST_KERNEL", "/fixture/kernel")
	t.Setenv("FAAS_E2E_SERVICE_HTTPS_CA", "")
	t.Setenv("GREGALE_CI_NATIVE_ACCEPTANCE", "1")
	if !nativeGuestDNS(APID|VMMD|Gatewayd) || nativeGuestDNS(APID|Gatewayd) {
		t.Fatal("CI guest DNS did not remain scoped to the native VM harness")
	}
}
