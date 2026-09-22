package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostsize"
	"github.com/onebox-faas/faas/pkg/role"
)

// nodeSizingReport is the deploy-layer contract for a host's derived
// capacity. node_join consumes it to write BOTH the systemd tenant-slice
// fence and the vmmd admission ceiling, so the kernel's hard limit and the
// scheduler's admission limit are always two views of one calculation.
//
// Before this existed, ansible carried its own `MemTotal * 85 / 100` formula
// while the cgroup fence was a hand-written drop-in no role owned. On the
// production 15,985 MB nodes that meant schedd admitted to 13,587 MB against
// a kernel fence of 8,192 MB, and the excess surfaced as CONSTRAINT_MEMCG
// OOM kills of tenant firecracker processes.
type nodeSizingReport struct {
	Role               string `json:"role"`
	HostMemTotalMB     int    `json:"host_mem_total_mb"`
	HostCPUs           int    `json:"host_cpus"`
	NonTenantReserveMB int    `json:"non_tenant_reserve_mb"`
	TenantSliceMaxMB   int    `json:"tenant_slice_max_mb"`
	TenantBudgetMB     int    `json:"tenant_budget_mb"`
	AdmissionCeilingMB int    `json:"admission_ceiling_mb"`
	VCPUSlots          int    `json:"vcpu_slots"`
	MemMB              int    `json:"mem_mb"`
	Detected           bool   `json:"detected"`
}

// nodeSizingShape maps a box role onto the reserve shape. Only compute-only
// drops the control-plane slice; an unknown or single-box role keeps the
// spec §13 reserve, because under-reserving an unrecognised host is the
// dangerous direction.
func nodeSizingShape(r role.Role) api.NodeRoleShape {
	if r == role.RoleComputeOnly {
		return api.NodeShapeComputeOnly
	}
	return api.NodeShapeSingleBox
}

// cmdComputeNodesSizing prints the capacity this host should advertise.
func cmdComputeNodesSizing(args []string) int {
	fs := flag.NewFlagSet("sizing", flag.ContinueOnError)
	fs.SetOutput(osStderr)
	roleFlag := fs.String("role", "", "box role: compute-only, control-plane, or single-box")
	// run() calls applyJSONFlag, which strips a bare --json from the argv and
	// sets the process-wide jsonOutput before dispatch. A subcommand-local
	// flag therefore never sees it, so both must be consulted — the same
	// `*jsonOut || jsonOutput` shape every other gregalectl command uses.
	jsonOut := fs.Bool("json", false, "emit the machine-readable report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	resolved := role.FromConfig(strings.TrimSpace(*roleFlag), "FAAS_VMMD_ROLE")
	memMB, cpus := hostsize.MemTotalMB(), hostsize.CPUs()
	sizing := api.DeriveNodeSizingForRole(memMB, cpus, nodeSizingShape(resolved))
	report := nodeSizingReport{
		Role:               string(resolved),
		HostMemTotalMB:     memMB,
		HostCPUs:           cpus,
		NonTenantReserveMB: sizing.NonTenantReserveMB,
		TenantSliceMaxMB:   sizing.TenantSliceMaxMB,
		TenantBudgetMB:     sizing.TenantBudgetMB,
		AdmissionCeilingMB: sizing.AdmissionCeilingMB,
		VCPUSlots:          sizing.VCPUSlots,
		MemMB:              sizing.MemMB,
		Detected:           memMB > 0,
	}
	if *jsonOut || jsonOutput {
		body, err := json.Marshal(report)
		if err != nil {
			_, _ = fmt.Fprintf(osStderr, "gregalectl compute-nodes sizing: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(osStdout, "%s\n", body)
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "role=%s detected=%t host_mem_total_mb=%d host_cpus=%d\n",
		report.Role, report.Detected, report.HostMemTotalMB, report.HostCPUs)
	_, _ = fmt.Fprintf(osStdout, "non_tenant_reserve_mb=%d tenant_slice_max_mb=%d tenant_budget_mb=%d admission_ceiling_mb=%d vcpu_slots=%d\n",
		report.NonTenantReserveMB, report.TenantSliceMaxMB, report.TenantBudgetMB, report.AdmissionCeilingMB, report.VCPUSlots)
	return 0
}
