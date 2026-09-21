package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/role"
)

// TestNodeJoinDerivesCapacityFromTheCLI pins ADR-203's deploy contract.
//
// node_join.yml used to carry `(ansible_memtotal_mb | int) * 85 / 100` as its
// own copy of the capacity arithmetic, while the tenant cgroup fence was a
// hand-written drop-in no role produced. On the production nodes that left
// schedd admitting 13,587 MB against a kernel fence of 8,192 MB. Both numbers
// must now come from `gregalectl compute-nodes sizing`.
//
// adr: 203
// spec: §13
func TestNodeJoinDerivesCapacityFromTheCLI(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatalf("read node_join.yml: %v", err)
	}
	play := string(body)

	// Match the Jinja expression, not the prose: the comment above the new
	// task deliberately quotes the old formula to explain what was removed.
	if strings.Contains(play, "(ansible_memtotal_mb | int) * 85 / 100") {
		t.Error("node_join.yml still carries its own capacity formula; it must use the sizing CLI")
	}
	for _, want := range []string{
		"compute-nodes\n          - sizing",
		"faas_node_sizing_raw.stdout | from_json",
		"faas_node_sizing.admission_ceiling_mb",
		"faas_node_sizing.tenant_slice_max_mb",
		"faas_node_sizing.detected | bool",
		"/etc/systemd/system/faas-tenant.slice.d/90-host-capacity.conf",
	} {
		if !strings.Contains(play, want) {
			t.Errorf("node_join.yml is missing the derived-capacity contract %q", want)
		}
	}

	// The admission ceiling and the cgroup fence must be two reads of one
	// report, never two independently computed numbers.
	ceiling := strings.Index(play, "FAAS_COMPUTE_ADMISSION_CEILING_MB={{ faas_join_admission_ceiling_mb | default(faas_node_sizing.admission_ceiling_mb, true) }}")
	fence := strings.Index(play, "MemoryMax={{ faas_node_sizing.tenant_slice_max_mb }}M")
	probe := strings.Index(play, "Derive this host's capacity from one source of truth")
	if ceiling < 0 || fence < 0 || probe < 0 {
		t.Fatalf("capacity contract incomplete: probe=%d fence=%d ceiling=%d", probe, fence, ceiling)
	}
	if !(probe < fence && probe < ceiling) {
		t.Fatalf("the sizing probe must run before both consumers: probe=%d fence=%d ceiling=%d", probe, fence, ceiling)
	}
}

// TestComputeNodesSizingReportMatchesTheLibrary pins that the CLI reports
// exactly what vmmd will derive, so the deploy layer and the daemon cannot
// disagree about a host's capacity.
//
// adr: 203
// spec: §13
func TestComputeNodesSizingReportMatchesTheLibrary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		role  role.Role
		shape api.NodeRoleShape
	}{
		{"compute-only", role.RoleComputeOnly, api.NodeShapeComputeOnly},
		{"control-plane", role.RoleControlPlane, api.NodeShapeSingleBox},
		{"single-box", role.RoleSingleBox, api.NodeShapeSingleBox},
	} {
		if got := nodeSizingShape(tc.role); got != tc.shape {
			t.Errorf("%s: shape = %v, want %v", tc.name, got, tc.shape)
		}
	}

	stdout, restore := captureSizingStdout(t)
	code := cmdComputeNodesSizing([]string{"--role", "compute-only", "--json"})
	out := restore()
	if code != 0 {
		t.Fatalf("sizing exit = %d, output %q", code, out)
	}
	var report nodeSizingReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
		t.Fatalf("parse report %q: %v", out, err)
	}
	_ = stdout
	if report.Role != string(role.RoleComputeOnly) {
		t.Fatalf("report role = %q, want compute-only", report.Role)
	}
	// Whatever this machine is, the report must equal the library's answer
	// for the same inputs. That equality is the whole contract.
	want := api.DeriveNodeSizingForRole(report.HostMemTotalMB, report.HostCPUs, api.NodeShapeComputeOnly)
	if report.AdmissionCeilingMB != want.AdmissionCeilingMB ||
		report.TenantSliceMaxMB != want.TenantSliceMaxMB ||
		report.TenantBudgetMB != want.TenantBudgetMB ||
		report.NonTenantReserveMB != want.NonTenantReserveMB {
		t.Fatalf("report %+v does not match library %+v", report, want)
	}
}

// captureSizingStdout swaps the package stdout seam for the duration of one
// command and returns what it wrote.
func captureSizingStdout(t *testing.T) (*os.File, func() string) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := osStdout
	osStdout = write
	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, rerr := read.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if rerr != nil {
				break
			}
		}
		done <- sb.String()
	}()
	return write, func() string {
		_ = write.Close()
		osStdout = prev
		out := <-done
		_ = read.Close()
		return out
	}
}

// TestComputeNodesSizingEmitsJSONThroughRun is the regression test for the
// bug that broke the ADR-203 deploy contract on the first real rollout.
//
// run() calls applyJSONFlag, which strips a bare --json from the argv and
// sets the process-wide jsonOutput *before* dispatch. The subcommand's own
// flag therefore never sees it, so `gregalectl compute-nodes sizing --json`
// printed the human form and node_join's `from_json` got an empty string:
//
//	the field 'args' has an invalid value ... Expecting value: line 1 column 1
//
// The original unit test missed this because it called the command function
// directly and skipped applyJSONFlag entirely. This one goes through run().
//
// adr: 203
// spec: §13
func TestComputeNodesSizingEmitsJSONThroughRun(t *testing.T) {
	prevJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = prevJSON })

	for _, args := range [][]string{
		{"compute-nodes", "sizing", "--role", "compute-only", "--json"},
		{"compute-nodes", "sizing", "--json", "--role", "compute-only"},
		{"--json", "compute-nodes", "sizing", "--role", "compute-only"},
	} {
		jsonOutput = false
		_, restore := captureSizingStdout(t)
		code := run(append([]string(nil), args...))
		out := restore()
		if code != 0 {
			t.Fatalf("%v: exit = %d, output %q", args, code, out)
		}
		var report nodeSizingReport
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
			t.Fatalf("%v: output is not JSON (%v): %q", args, err, out)
		}
		if report.Role != string(role.RoleComputeOnly) {
			t.Fatalf("%v: role = %q, want compute-only", args, report.Role)
		}
	}

	// Without the flag the human form is still the default, so an operator
	// reading the terminal is unaffected.
	jsonOutput = false
	_, restore := captureSizingStdout(t)
	code := run([]string{"compute-nodes", "sizing", "--role", "compute-only"})
	out := restore()
	if code != 0 {
		t.Fatalf("human form exit = %d, output %q", code, out)
	}
	if strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Fatalf("human form emitted JSON: %q", out)
	}
	if !strings.Contains(out, "admission_ceiling_mb=") {
		t.Fatalf("human form is missing the ceiling: %q", out)
	}
}
