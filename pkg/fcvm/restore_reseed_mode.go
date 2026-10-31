package fcvm

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// WithRestoreReseedThread boots app VMs with api.RestoreReseedThreadBootArg,
// so their Node processes answer the restore reseed barrier from the
// preload addon's native thread instead of the event loop
// (ADR-20261024105607). A restored VM keeps the mode its snapshot's
// guest-init booted with: switching it changes only later cold boots and
// the snapshots they capture. Both modes reseed before the resume ACK.
func (v *JailerVMM) WithRestoreReseedThread(enabled bool) *JailerVMM {
	v.restoreReseedThread = enabled
	return v
}

// withRestoreReseedMode adds the thread-mode boot argument to an app VM's
// cold boot when the VMM has it enabled. Builders and networkless execution
// guests run no barrier, so they keep their command line.
func (v *JailerVMM) withRestoreReseedMode(l Lease, cfg VMConfig) VMConfig {
	if !v.restoreReseedThread || l.IsBuilder || l.Networkless ||
		strings.Contains(cfg.BootSource.BootArgs, api.RestoreReseedThreadBootArg) {
		return cfg
	}
	cfg.BootSource.BootArgs = strings.TrimSpace(cfg.BootSource.BootArgs) + " " + api.RestoreReseedThreadBootArg
	return cfg
}
