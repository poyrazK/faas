//go:build linux || darwin

package fcvm

// adr: 20261016
// Repair results require verified guest cleanup before publication.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
	repairguest "github.com/onebox-faas/faas/pkg/datarepair/guest"
	"github.com/onebox-faas/faas/pkg/netns"
)

type dataRepairGuestFixture struct {
	*recoveryVMMFixture
	entered chan struct{}
	hold    bool
}

func (v *dataRepairGuestFixture) clearCgroupFiles(lease Lease) {
	// Ordinary files stand in for kernel pseudo-files in this portable fixture.
	dir := nativeCgroupScope(lease)
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if !entry.IsDir() {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
func (v *dataRepairGuestFixture) DestroyWithExport(ctx context.Context, l Lease, out string) (int, error) {
	v.clearCgroupFiles(l)
	return v.recoveryVMMFixture.DestroyWithExport(ctx, l, out)
}
func (v *dataRepairGuestFixture) Kill(ctx context.Context, l Lease) error {
	v.clearCgroupFiles(l)
	return v.recoveryVMMFixture.Kill(ctx, l)
}

func (v *dataRepairGuestFixture) DialDataRepairRehearsal(_ context.Context, _ Lease) (net.Conn, error) {
	host, guest := net.Pipe()
	go func() {
		defer guest.Close()
		in, err := core.ReadRehearsalBody(guest)
		if err != nil {
			return
		}
		if v.entered != nil {
			close(v.entered)
		}
		if v.hold {
			_, _ = io.Copy(io.Discard, guest)
			return
		}
		_ = json.NewEncoder(guest).Encode(repairguest.Result{Evidence: &core.Evidence{Passed: true, PlanDigest: in.Plan.Digest, ChecksSHA256: in.Plan.ChecksSHA256, SnapshotSHA256: in.SnapshotSHA256, Runner: core.MicroVMRunner}})
		_, _ = io.Copy(io.Discard, guest)
	}()
	return host, nil
}

func dataRepairNativeFixture(t *testing.T) (*Manager, *JailerVMM, core.RehearsalFrame, core.RehearsalInput, *dataRepairGuestFixture) {
	t.Helper()
	m, v, _ := nativeManagerFixture(t)
	write := func(name string) (string, string) {
		path := filepath.Join(t.TempDir(), name)
		b := []byte("immutable " + name)
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		return path, hex.EncodeToString(h[:])
	}
	kernel, kh := write("kernel")
	base, bh := write("base")
	layer, lh := write("layer")
	m.paths.Kernel = kernel
	spec := core.RunnerSpec{BaseKey: base, LayerKey: layer, KernelSHA256: kh, BaseSHA256: bh, LayerSHA256: lh}
	node := uuid.NewString()
	m.WithRuntimeAdmissionNodeID(node)
	m.WithDataRepairRehearsals(node, spec)
	_, inc, _, err := m.DataRepairRehearsalIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dump := []byte("snapshot")
	dh := sha256.Sum256(dump)
	ch := sha256.Sum256([]byte("SELECT true"))
	p := core.Plan{ID: uuid.NewString(), Digest: strings.Repeat("a", 64), ChecksSHA256: hex.EncodeToString(ch[:]), Candidates: 1, ExpiresAt: time.Now().UTC().Add(time.Minute), Scope: core.Scope{AccountID: uuid.NewString(), AppID: uuid.NewString(), Environment: "production"}}
	in := core.RehearsalInput{Plan: p, CheckSQL: "SELECT true", Database: "postgres", Dump: dump, SnapshotSHA256: hex.EncodeToString(dh[:])}
	f := core.RehearsalFrame{TaskID: uuid.NewString(), OperationID: p.ID, Scope: p.Scope, Binding: strings.Repeat("b", 64), NodeID: node, Incarnation: inc, PlanID: p.ID, PlanDigest: p.Digest, ChecksSHA256: p.ChecksSHA256, SnapshotSHA256: in.SnapshotSHA256, PlanExpiresAt: p.ExpiresAt, Deadline: p.ExpiresAt, Plan: api.PlanPro, Runner: spec}
	guest := &dataRepairGuestFixture{recoveryVMMFixture: m.vmm.(*recoveryVMMFixture)}
	m.vmm = guest
	return m, v, f, in, guest
}

func TestDataRepairNativeCleanupAndReplayFence(t *testing.T) {
	m, v, f, in, guest := dataRepairNativeFixture(t)
	e, err := m.RunDataRepairRehearsal(t.Context(), f, in)
	if err != nil || !f.ValidEvidence(e) {
		t.Fatalf("rehearse: %+v %v; cleanup: %v", e, err, m.Destroy(t.Context(), f.TaskID))
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("successful evidence preceded cleanup")
	}
	if len(guest.coldBootSpecs) != 1 || !guest.coldBootSpecs[0].Networkless || guest.coldBootSpecs[0].AppTask {
		t.Fatal("rehearsal inherited app runtime")
	}
	for _, s := range guest.stagedSecrets {
		if string(s.blob) != "{}" {
			t.Fatal("rehearsal inherited secrets")
		}
	}
	r, err := v.nativeRecovery.journal.read(f.TaskID)
	if err != nil || !r.ResourcesRemoved {
		t.Fatal("native cleanup proof missing", err)
	}
	if _, err = m.RunDataRepairRehearsal(t.Context(), f, in); err == nil {
		t.Fatal("replayed VM")
	}
	if _, err = m.RetireDataRepairRehearsal(t.Context(), f); err != nil {
		t.Fatal("idempotent retirement", err)
	}
	if len(guest.coldBootSpecs) != 1 {
		t.Fatal("cleanup replay booted a VM")
	}
}

func TestDataRepairNativeFailedCleanupWithholdsEvidence(t *testing.T) {
	m, v, f, in, _ := dataRepairNativeFixture(t)
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return errors.New("busy") }
	e, err := m.RunDataRepairRehearsal(t.Context(), f, in)
	if err == nil || e.Passed || e.RetiredAt != nil {
		t.Fatal("unconfirmed teardown emitted evidence")
	}
	if m.LeasedCount() == 0 {
		t.Fatal("failed teardown released lease")
	}
	v.nativeRecovery.resources = func(Lease, netns.Config) error { return nil }
	if _, err = m.RetireDataRepairRehearsal(t.Context(), f); err != nil {
		t.Fatal("recovery", err)
	}
	if m.LeasedCount() != 0 {
		t.Fatal("recovery retained resources")
	}
}

func TestDataRepairNativeRetireFencesDelayedBootAndChangedFrames(t *testing.T) {
	m, _, f, in, guest := dataRepairNativeFixture(t)
	if _, err := m.RetireDataRepairRehearsal(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RunDataRepairRehearsal(t.Context(), f, in); err == nil {
		t.Fatal("late boot survived revocation")
	}
	changed := f
	changed.Scope.AppID = uuid.NewString()
	if _, err := m.RetireDataRepairRehearsal(t.Context(), changed); err == nil {
		t.Fatal("different frame adopted tombstone")
	}
	if len(guest.coldBootSpecs) != 0 || m.LeasedCount() != 0 {
		t.Fatal("revoked attempt allocated")
	}
}

func TestDataRepairNativeCancelJoinsGuestAndCleanup(t *testing.T) {
	m, _, f, in, guest := dataRepairNativeFixture(t)
	guest.entered = make(chan struct{})
	guest.hold = true
	done := make(chan error, 1)
	go func() { _, err := m.RunDataRepairRehearsal(t.Context(), f, in); done <- err }()
	select {
	case <-guest.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("guest never received input")
	}
	if _, err := m.RetireDataRepairRehearsal(t.Context(), f); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("cancelled guest succeeded")
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("cancel ACK preceded cleanup")
	}
}

func TestDataRepairNativeOldDaemonCannotCreate(t *testing.T) {
	m, _, f, in, guest := dataRepairNativeFixture(t)
	m.runtimeAdmissionIncarnation = uuid.NewString()
	if _, err := m.RunDataRepairRehearsal(t.Context(), f, in); err == nil {
		t.Fatal("old daemon frame booted")
	}
	if len(guest.coldBootSpecs) != 0 {
		t.Fatal("old daemon reached allocation")
	}
	if _, err := m.RetireDataRepairRehearsal(t.Context(), f); err == nil {
		t.Fatal("missing old-daemon journal was treated as cleanup proof")
	}
}

func TestDataRepairNativeJournalInventoryAndProducerFence(t *testing.T) {
	m, v, f, in, _ := dataRepairNativeFixture(t)
	if _, err := m.RunDataRepairRehearsal(t.Context(), f, in); err != nil {
		t.Fatal(err)
	}
	if _, err := v.nativeRecovery.journal.records(t.Context()); err != nil {
		t.Fatal("restart inventory rejected retired task", err)
	}
	physical, err := v.nativeRecovery.journal.read(f.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if err = v.nativeRecovery.journal.prepare(t.Context(), physical.Lease); err == nil {
		t.Fatal("generic producer borrowed repair identity")
	}
	if _, err = v.nativeRecovery.journal.replace(t.Context(), physical.Lease, physical.Generation, true); err == nil {
		t.Fatal("physical repair generation was replaced")
	}
	path, err := m.dataRepairPath(t.Context(), f)
	if err != nil {
		t.Fatal(err)
	}
	r, err := readDataRepairRecord(path)
	if err != nil {
		t.Fatal(err)
	}
	r.NativeGeneration = uuid.NewString()
	if err = writeNativeJournalValue(path, r); err != nil {
		t.Fatal(err)
	}
	if _, err = v.nativeRecovery.journal.records(t.Context()); err == nil {
		t.Fatal("startup ignored a changed task/physical binding")
	}
}

func TestDataRepairNativeChangedImageHasNoAllocation(t *testing.T) {
	m, _, f, in, guest := dataRepairNativeFixture(t)
	if err := os.WriteFile(f.Runner.BaseKey, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if e, err := m.RunDataRepairRehearsal(t.Context(), f, in); err == nil || e.Passed {
		t.Fatal("changed image produced evidence")
	}
	if _, err := m.RetireDataRepairRehearsal(t.Context(), f); err != nil {
		t.Fatal("retire before allocation", err)
	}
	if len(guest.coldBootSpecs) != 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("changed image allocated resources")
	}
}
