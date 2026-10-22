// adr: 20261022090000 — native builder export authority and enrollment.
package fcvm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeBuildExportDestinationAuthority(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	v := &JailerVMM{chrootBase: filepath.Join(base, "jail")}
	exports := filepath.Join(base, "exports")
	if err := os.MkdirAll(exports, 0o755); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(exports, "build-1")
	if err := v.claimNativeBuildExportDestination(fresh); err != nil {
		t.Fatalf("a fresh destination under a real parent was refused: %v", err)
	}
	if info, err := os.Lstat(fresh); err != nil || !info.IsDir() {
		t.Fatalf("destination was not created: %v", err)
	}
	if err := v.claimNativeBuildExportDestination(fresh); err != nil {
		t.Fatalf("an empty pre-created destination was refused: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fresh, "stale"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(exports, linked); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(exports, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, dir := range map[string]string{
		"relative":         "exports/build-2",
		"unclean":          exports + "/../exports/build-2",
		"root":             "/",
		"inside jail":      filepath.Join(v.chrootBase, "build-2"),
		"symlinked parent": filepath.Join(linked, "build-2"),
		"missing parent":   filepath.Join(base, "absent", "build-2"),
		"populated":        fresh,
		"not a directory":  file,
		"link as the leaf": linked,
	} {
		if err := v.claimNativeBuildExportDestination(dir); err == nil {
			t.Errorf("%s destination %q was accepted", name, dir)
		}
	}
}

func TestNativeBuilderProducerEnrollmentFollowsTheLease(t *testing.T) {
	producer := nativeProducerIdentity{Protocol: nativeBuilderExportProducerProtocol, PID: 4242, UID: 0, StartTime: 7}
	builder := leaseForSlot("build-0001", 3)
	builder.IsBuilder, builder.Plan = true, api.PlanHobby
	record := nativeLaunchRecord{Version: 2, Lease: builder, Producer: producer}
	if err := record.validateProducer(); err != nil {
		t.Fatalf("an enrolled builder was refused: %v", err)
	}
	for name, mutate := range map[string]func(*nativeLaunchRecord){
		"not a builder":    func(r *nativeLaunchRecord) { r.Lease.IsBuilder = false },
		"networkless":      func(r *nativeLaunchRecord) { r.Lease.Networkless = true },
		"version one":      func(r *nativeLaunchRecord) { r.Version = 1 },
		"repair protocol":  func(r *nativeLaunchRecord) { r.Producer.Protocol = nativeRepairProducerProtocol },
		"unknown protocol": func(r *nativeLaunchRecord) { r.Producer.Protocol = "builder-export-v0" },
	} {
		changed := record
		mutate(&changed)
		if err := changed.validateProducer(); err == nil {
			t.Errorf("%s: builder enrollment accepted", name)
		}
	}
}

// Builder enrollment never needs a data-repair parent at startup; repair
// enrollment still does.
func TestDataRepairStartupIgnoresBuilderProducers(t *testing.T) {
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	builder := leaseForSlot("build-0001", 3)
	builder.IsBuilder = true
	physical := []nativeLaunchRecord{{Version: 2, Lease: builder,
		Producer: nativeProducerIdentity{Protocol: nativeBuilderExportProducerProtocol, PID: 1, StartTime: 1}}}
	if err := j.validateDataRepairRecords(t.Context(), physical); err != nil {
		t.Fatalf("a builder producer required a repair record: %v", err)
	}
	physical[0].Producer.Protocol = nativeRepairProducerProtocol
	if err := j.validateDataRepairRecords(t.Context(), physical); err == nil {
		t.Fatal("a repair producer without its repair record was accepted")
	}
}

// Export frames carry read_only; staging frames written before it still decode.
func TestNativeLoopMountExportFrameDecoding(t *testing.T) {
	j, lease := preparedNativeJournal(t)
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	record := nativeLoopMountRecord{ID: "bd563861-501b-4ac2-9b4a-e2b782b3c0ad", Owner: owner, ReadOnly: true,
		Device: nativeLoopDevice{Number: 7, Rdev: 99, Source: nativeLoopIdentity{Device: 17, Inode: 42}, Namespace: nativeLoopIdentity{Device: 19, Inode: 43}}}
	raw, err := json.Marshal(record)
	if err != nil || !strings.Contains(string(raw), `"read_only":true`) {
		t.Fatalf("export frame = %s %v", raw, err)
	}
	var decoded nativeLoopMountRecord
	if err := json.Unmarshal(raw, &decoded); err != nil || !decoded.ReadOnly {
		t.Fatalf("export frame did not round-trip: %+v %v", decoded, err)
	}
	record.ReadOnly = false
	raw, err = json.Marshal(record)
	if err != nil || strings.Contains(string(raw), "read_only") {
		t.Fatalf("staging frame gained read_only: %s %v", raw, err)
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.ReadOnly {
		t.Fatalf("staging frame did not decode: %+v %v", decoded, err)
	}
	if err := json.Unmarshal([]byte(strings.Replace(string(raw), `"removed":false`, `"removed":false,"extra":1`, 1)), &decoded); err == nil {
		t.Fatal("an unknown frame field was accepted")
	}
}
