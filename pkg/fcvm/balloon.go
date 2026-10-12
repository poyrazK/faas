package fcvm

import "golang.org/x/mod/semver"

// Balloon is the Firecracker balloon device (`balloon` in the config file).
// Gregale never inflates it: AmountMiB stays 0, so the guest keeps all of
// its plan RAM. The device exists for free page reporting (ADR-985), which
// lets the guest hand pages it has freed back to the host.
type Balloon struct {
	AmountMiB             int  `json:"amount_mib"`
	DeflateOnOOM          bool `json:"deflate_on_oom"`
	StatsPollingIntervalS int  `json:"stats_polling_interval_s"`
	FreePageReporting     bool `json:"free_page_reporting"`
}

// freePageReportingMinFirecracker is the first release with balloon free
// page reporting. Older releases reject the field and would fail every boot.
const freePageReportingMinFirecracker = "v1.14.0"

// FreePageReportingSupported reports whether a detected Firecracker version
// ("1.17.0") accepts free page reporting.
func FreePageReportingSupported(fcVersion string) bool {
	v := "v" + fcVersion
	return semver.IsValid(v) && semver.Compare(v, freePageReportingMinFirecracker) >= 0
}

// WithFreePageReporting attaches a free-page-reporting balloon to every VM
// this VMM cold boots (ADR-985). Restored VMs keep whatever devices their
// snapshot recorded, so snapshots taken before the switch restore unchanged.
// The caller must check FreePageReportingSupported first.
func (v *JailerVMM) WithFreePageReporting(enabled bool) *JailerVMM {
	v.freePageReporting = enabled
	return v
}

// withBalloon returns cfg with the free-page-reporting balloon attached when
// the VMM has it enabled. A caller-supplied balloon is left untouched.
func (v *JailerVMM) withBalloon(cfg VMConfig) VMConfig {
	if v.freePageReporting && cfg.Balloon == nil {
		cfg.Balloon = &Balloon{DeflateOnOOM: true, FreePageReporting: true}
	}
	return cfg
}
