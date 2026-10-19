package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"testing"
)

// TestTierCAppsRmUsage pins the dispatcher shape of cmdAppsRm:
// missing positional argument => usage banner + rc=1.
func TestTierCAppsRmUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdAppsRm(nil); code != 1 {
		t.Errorf("cmdAppsRm() = %d, want 1", code)
	}
}

// TestTierCRollbackUsage pins cmdRollback's argument count check.
func TestTierCRollbackUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdRollback(nil); code != 1 {
		t.Errorf("cmdRollback() = %d, want 1", code)
	}
}

// TestTierCParkUsage pins cmdPark's single-arg requirement.
func TestTierCParkUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdPark(nil); code != 1 {
		t.Errorf("cmdPark() = %d, want 1", code)
	}
}

// TestTierCWakeUsage pins cmdWake's single-arg requirement.
func TestTierCWakeUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdWake(nil); code != 1 {
		t.Errorf("cmdWake() = %d, want 1", code)
	}
}

// TestTierCDomainsUsage pins cmdDomains's argument count check.
func TestTierCDomainsUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdDomains(nil); code != 1 {
		t.Errorf("cmdDomains() = %d, want 1", code)
	}
}

// TestTierCKeysUsage pins cmdKeys's argument count check.
func TestTierCKeysUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdKeys(nil); code != 1 {
		t.Errorf("cmdKeys() = %d, want 1", code)
	}
}

// TestTierCInvoicesUsage pins cmdInvoices's unauthorized branch.
// cmdInvoices doesn't take a positional slug; it parses flags then
// calls authedClient(). With no FAAS_TOKEN, authedClient returns an
// error and cmdInvoices returns a non-zero exit. The exact code is
// order-dependent (other tests may have flipped jsonOutput before
// this one runs, which changes the printErr path) so we only assert
// the unauthorized outcome, not the specific code.
func TestTierCInvoicesUsage(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("FAAS_TOKEN", "")
	t.Setenv("FAAS_API", "")
	_ = os.Unsetenv // keep import
	if code := main.CmdInvoices(nil); code == 0 {
		t.Errorf("cmdInvoices() = 0, want non-zero (unauthorized)")
	}
}

// TestTierCBackupUsage, TestTierCHostAgeInitUsage,
// TestTierCHostAgeRotateUsage, and TestTierCHostAgePrunePreviousUsage
// were deleted in PR-6.5 (they covered operator-side dispatchers that
// moved to cmd/gregalectl/ — coverage now lives in
// cmd/gregalectl/commands_backup_test.go and
// cmd/gregalectl/commands_host_age_test.go).

// TestTierCBuildUsage pins cmdBuild's argument count check.
func TestTierCBuildUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdBuild(nil); code != 1 {
		t.Errorf("cmdBuild() = %d, want 1", code)
	}
}

// TestTierCBuildSbomUsage pins cmdBuildSbom's argument count check.
func TestTierCBuildSbomUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdBuildSbom(nil); code != 1 {
		t.Errorf("cmdBuildSbom() = %d, want 1", code)
	}
}

// TestTierCBuildProvenanceUsage pins cmdBuildProvenance's argument count check.
func TestTierCBuildProvenanceUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdBuildProvenance(nil); code != 1 {
		t.Errorf("cmdBuildProvenance() = %d, want 1", code)
	}
}

// TestTierCInvocationsUsage pins cmdInvocations's argument count check.
func TestTierCInvocationsUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdInvocations(nil); code != 1 {
		t.Errorf("cmdInvocations() = %d, want 1", code)
	}
}

// TestTierCOrgsKeysUsage pins cmdOrgsKeys's argument count check.
func TestTierCOrgsKeysUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdOrgsKeys(nil); code != 1 {
		t.Errorf("cmdOrgsKeys() = %d, want 1", code)
	}
}

// TestTierCAlertInfoUsage pins cmdAlertInfo's argument count check.
func TestTierCAlertInfoUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdAlertInfo(nil); code != 1 {
		t.Errorf("cmdAlertInfo() = %d, want 1", code)
	}
}

// TestTierCAlertRmUsage pins cmdAlertRm's argument count check.
func TestTierCAlertRmUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdAlertRm(nil); code != 1 {
		t.Errorf("cmdAlertRm() = %d, want 1", code)
	}
}

// TestTierCOrgsKeysInfoUsage pins cmdOrgsKeysInfo's argument count check.
func TestTierCOrgsKeysInfoUsage(t *testing.T) {
	main.ResetJSONOut(t)
	if code := main.CmdOrgsKeysInfo(nil); code != 1 {
		t.Errorf("cmdOrgsKeysInfo() = %d, want 1", code)
	}
}
