//go:build metal && linux

// adr: 20261114010000 — real daemon crash boundaries and native owner recovery.
package runlifecycle_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

type templateBarrier struct{ Stage, Instance, Memory string }
type templatePhysical struct {
	Version                  int
	Generation, KernelBootID string
	Revoked                  bool `json:"revoked"`
	ExitConfirmed            bool `json:"exit_confirmed"`
	ResourcesRemoved         bool `json:"resources_removed"`
	Lease                    struct {
		Instance               string
		Networkless, IsBuilder bool
	}
}

func TestSourceNativeRecoveryMetal(t *testing.T) {
	if os.Getenv("FAAS_RUN_LIFECYCLE_NATIVE_RECOVERY") != "1" {
		t.Fatal("native recovery acceptance profile is required")
	}
	f := newRunFixture(t)
	for _, tc := range []struct{ name, stage string }{
		{"crash_before_boot", "flight"}, {"crash_after_pristine_ack", "pristine"}, {"crash_after_pause", "paused"}, {"crash_during_publication", "memory_published"}, {"crash_after_capture", "complete"},
	} {
		if !t.Run(tc.name, func(t *testing.T) { f.crashTemplate(t, tc.stage) }) {
			return
		}
	}
	var published sched.RuntimeSnapshot
	if !t.Run("schedd_crash_during_capture", func(t *testing.T) {
		f.armTemplate(t, "paused")
		for key, value := range map[string]string{"FAAS_EXECUTION_TEMPLATE_CAPTURE": "1", "FAAS_EXECUTION_TEMPLATE_ACCOUNT_ID": f.templateAccountID, "FAAS_EXECUTION_TEMPLATE_SHAPES": `[{"runtime":"node24","memory_mb":256,"ephemeral_disk_mb":64}]`} {
			if err := f.h.SetScheddEnv(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := f.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		point := f.waitTemplateBarrier(t, "paused")
		if err := f.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		f.disarmTemplate(t)
		deadline := time.Now().Add(30 * time.Second)
		for {
			if _, err := os.Lstat(f.templateFlightPath(point.Instance)); errors.Is(err, os.ErrNotExist) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("cancelled capture retained its flight")
			}
			time.Sleep(50 * time.Millisecond)
		}
		f.requireTemplateRetired(t, point)
		f.requireNoTemplateRow(t)
		f.collectUnpublishedTemplate(t, point.Memory)
		if err := f.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		key, _ := f.identity.Key()
		index := sched.NewStateRuntimeSnapshotIndex(state.NewPgStore(f.h.Pool))
		deadline = time.Now().Add(90 * time.Second)
		for {
			var err error
			published, err = index.LookupRuntimeSnapshot(t.Context(), key)
			if err == nil {
				break
			}
			if !errors.Is(err, sched.ErrRuntimeSnapshotNotFound) || time.Now().After(deadline) {
				t.Fatalf("fresh worker publication: %v", err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if published.StorageKey == point.Memory {
			t.Fatal("interrupted capture was promoted")
		}
		f.requireCleanRestore(t, "after-native-schedd-capture-restart")
		t.Log("native recovery witness: phase=schedd_capture physical_retired=true old_capture_promoted=false fresh_publication=true")
	}) {
		return
	}
	if !t.Run("published_template_survives_owner_restart", func(t *testing.T) {
		if err := f.h.RestartVMMD(); err != nil {
			t.Fatal(err)
		}
		if err := f.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		got := f.capturePristine(t)
		if got.StorageKey != published.StorageKey || got.SnapshotDigest != published.SnapshotDigest {
			t.Fatal("restart changed committed publication")
		}
		f.requireCleanRestore(t, "after-native-published-owner-restart")
		t.Log("native recovery witness: phase=published_restart catalog_unchanged=true restored=true")
	}) {
		return
	}
	for _, owner := range []string{"vmmd", "schedd"} {
		if !t.Run(owner+"_restart_never_reexecutes_running_work", func(t *testing.T) {
			key := "native-owner-" + owner
			req := runRequest(`export default async()=>{console.log('running-marker');await new Promise(r=>setTimeout(r,25000));return {unexpected:true}}`)
			row := f.submit(t, key, req)
			f.waitGuestStarted(t, row.ID)
			f.requireWakeMethod(t, row.ID, "restore")
			var err error
			if owner == "vmmd" {
				err = f.h.RestartVMMD()
			} else {
				err = f.h.RestartSchedd()
			}
			if err != nil {
				t.Fatal(err)
			}
			settled := f.wait(t, row.ID, "terminal")
			if settled.Status == api.ExecutionStatusSucceeded || f.guestMarkerCount(t, row.ID) != 1 {
				t.Fatal("owner restart replayed or completed interrupted work")
			}
			if replay := f.submit(t, key, req); replay.ID != row.ID || !replay.Status.Terminal() {
				t.Fatal("owner restart lost the original receipt")
			}
			f.requireCleanRestore(t, "after-native-running-"+owner)
			t.Logf("native recovery witness: phase=running_%s guest_executions=1 original_receipt=true physical_retired=true", owner)
		}) {
			return
		}
	}
	t.Run("payload_erasure_and_single_usage_entry", func(t *testing.T) {
		usage, err := state.NewPgStore(f.h.Pool).ExecutionUsageByAccount(t.Context(), f.templateAccountID, time.Now().UTC())
		if err != nil || usage.Runs != 0 {
			t.Fatalf("template account charged Runs: %+v %v", usage, err)
		}
		f.assertSettled(t)
		t.Logf("native recovery settlement: customer_runs=%d template_runs=0", len(f.runs))
	})
}

func (f *runFixture) armTemplate(t *testing.T, stage string) {
	t.Helper()
	for _, name := range []string{"armed", "reached.json", "release"} {
		if err := os.Remove(filepath.Join(f.barrierRoot, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.barrierRoot, "armed"), []byte(stage+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
func (f *runFixture) disarmTemplate(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.barrierRoot, "armed")); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func (f *runFixture) waitTemplateBarrier(t *testing.T, stage string, failed ...<-chan error) templateBarrier {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if len(failed) > 0 {
			select {
			case err := <-failed[0]:
				t.Fatalf("capture ended before barrier %s: %v", stage, err)
			default:
			}
		}
		data, err := os.ReadFile(filepath.Join(f.barrierRoot, "reached.json")) //nolint:forbidigo // This test's root-only, metal-only barrier.
		if err == nil {
			var point templateBarrier
			if json.Unmarshal(data, &point) != nil || point.Stage != stage || uuid.Validate(point.Instance) != nil {
				t.Fatal("invalid native barrier")
			}
			parts := strings.Split(point.Memory, "/")
			if len(parts) != 6 || parts[0] != "snap" || parts[1] != uuid.Nil.String() || parts[2] != "captures" || parts[4] != "v2" || parts[5] != "mem" || uuid.Validate(parts[3]) != nil {
				t.Fatal("invalid barrier namespace")
			}
			return point
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("native capture never reached armed barrier")
	return templateBarrier{}
}
func (f *runFixture) templateFlightPath(instance string) string {
	return filepath.Join(fcvm.JailChrootBase, ".native-processes", "runtime-captures", "templates", instance+".json")
}
func (f *runFixture) requireTemplateRetired(t *testing.T, point templateBarrier) {
	t.Helper()
	if _, err := os.Lstat(f.templateFlightPath(point.Instance)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("recovered flight retained live authority", err)
	}
	path := filepath.Join(fcvm.JailChrootBase, ".native-processes", point.Instance+".json")
	data, err := os.ReadFile(path) //nolint:forbidigo // Validated fixture UUID under this namespace's private native journal.
	if point.Stage == "flight" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal("pre-boot crash acquired physical ownership", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	var physical templatePhysical
	if json.Unmarshal(data, &physical) != nil || physical.Version != 3 || physical.Lease.Instance != point.Instance || !physical.Lease.Networkless || physical.Lease.IsBuilder || !physical.Revoked || !physical.ExitConfirmed || !physical.ResourcesRemoved {
		t.Fatal("original physical owner was not fully retired")
	}
}
func (f *runFixture) requireNoTemplateRow(t *testing.T) {
	t.Helper()
	key, _ := f.identity.Key()
	if _, err := state.NewPgStore(f.h.Pool).LookupRuntimeSnapshot(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("uncertain capture became a catalog publication", err)
	}
}
func (f *runFixture) collectUnpublishedTemplate(t *testing.T, memory string) {
	t.Helper()
	directory := filepath.Dir(filepath.Dir(filepath.Join(f.nativeRoot, memory)))
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(directory, old, old); err != nil {
		t.Fatal(err)
	}
	client := imaged.NewVMMClient(f.h.VMMDSock, nil)
	defer client.Close()
	loop := imaged.NewLoop(imaged.LoopConfig{Handler: (&imaged.Handler{}).WithNativeRuntimeSnapshotRoot(f.nativeRoot).WithVMMClient(client), Store: state.NewPgStore(f.h.Pool), StorageRoot: f.storageRoot})
	if err := loop.CollectExecutionTemplateOrphans(t.Context(), time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, key := range templateObjectKeys(memory) {
		if _, err := os.Lstat(filepath.Join(f.nativeRoot, key)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unpublished object survived catalog-aware GC", err)
		}
	}
}
func (f *runFixture) crashTemplate(t *testing.T, stage string) {
	t.Helper()
	f.armTemplate(t, stage)
	spec := f.templateSpec(t)
	backend, err := f.snapshotBackend()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	conn, err := wire.DialContext(ctx, f.h.VMMDSock, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() {
		_, err := sched.NewExecutionTemplatePublisher(state.NewPgStore(f.h.Pool), backend, sched.NewVMMClient(conn)).Ensure(ctx, spec)
		done <- err
	}()
	point := f.waitTemplateBarrier(t, stage, done)
	if err := f.h.KillVMMD(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("crashed capture returned success")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("crashed RPC did not settle")
	}
	f.disarmTemplate(t)
	if err := f.h.RestartVMMD(); err != nil {
		t.Fatal(err)
	}
	f.requireTemplateRetired(t, point)
	f.requireNoTemplateRow(t)
	f.collectUnpublishedTemplate(t, point.Memory)
	row := f.wait(t, f.submit(t, "healthy-after-"+stage, runRequest(`export default async()=>({answer:42})`)).ID, api.ExecutionStatusSucceeded)
	f.requireWakeMethod(t, row.ID, "cold_boot")
	output, err := exec.CommandContext(t.Context(), "bash", "../../../deploy/scripts/leakcheck.sh").CombinedOutput()
	if err != nil {
		t.Fatalf("post-crash physical leakcheck: %v: %s", err, output)
	}
	t.Logf("native recovery witness: phase=%s physical_retired=true catalog_ready=false orphan_collected=true cold_fallback=true", stage)
}
