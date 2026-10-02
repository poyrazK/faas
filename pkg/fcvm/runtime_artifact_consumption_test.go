package fcvm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type consumedRuntimeVMM struct {
	runtimeSourceCapableVMM
	sources  []runtimeadmission.ArtifactSource
	spec     ColdBootSpec
	observed int
	edit     func(*RuntimeDriveHandoffObservation)
	observe  func(context.Context) error
}

func (v *consumedRuntimeVMM) SupportsRuntimeArtifactConsumption() bool { return true }

func (v *consumedRuntimeVMM) BootColdBootVerified(ctx context.Context, l Lease, spec ColdBootSpec, sources []runtimeadmission.ArtifactSource) error {
	v.sources, v.spec = slices.Clone(sources), spec
	return v.runtimeSourceCapableVMM.BootColdBootVerified(ctx, l, spec, sources)
}

func (v *consumedRuntimeVMM) ObservedRuntimeDrives(ctx context.Context, l Lease) (RuntimeDriveHandoffObservation, error) {
	v.observed++
	if v.observe != nil {
		if err := v.observe(ctx); err != nil {
			return RuntimeDriveHandoffObservation{}, err
		}
	}
	observation := RuntimeDriveHandoffObservation{InstanceID: l.Instance, LeaseUID: l.UID, ProcessPID: 42, ProcessStart: "101", ConfigHash: strings.Repeat("a", 64)}
	config := BuildColdBootConfig(v.spec, l.Slot)
	for i, drive := range config.Drives {
		source := v.sources[i]
		identity := rootfs.ArtifactIdentity{Digest: source.Digest, Bytes: source.Bytes}
		observation.Drives = append(observation.Drives, RuntimeDriveObservation{Source: source, DriveID: drive.DriveID, ReadOnly: drive.IsReadOnly, RootDevice: drive.IsRootDevice, Producer: identity, Injected: identity})
	}
	if v.edit != nil {
		v.edit(&observation)
	}
	return observation, nil
}

func consumedRuntimeFixture(t *testing.T) (*Manager, *consumedRuntimeVMM, AdmittedWakeRequest) {
	t.Helper()
	v := &consumedRuntimeVMM{runtimeSourceCapableVMM: runtimeSourceCapableVMM{fakeVMM: &fakeVMM{}}}
	m := newTestManager(&fakeRunner{}, v)
	r := admittedSourceFixture(t, m)
	r.Binding.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	var err error
	r.Binding.ArtifactSourcesHash, err = runtimeadmission.HashArtifactSources(r.Request.ArtifactSources)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), r.Request.Instance) })
	return m, v, r
}

func TestNativeArtifactConsumptionRequiresBackendObservationAndOwnsReceipt(t *testing.T) {
	m, v, request := consumedRuntimeFixture(t)
	identity, err := m.RuntimeAdmissionIdentity()
	if err != nil || identity.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion {
		t.Fatal("measured backend did not advertise its versioned capability", err)
	}
	inst, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil || inst == nil || receipt.Check(request.Binding, time.Now()) != nil || v.observed != 1 || len(receipt.ArtifactConsumption.Drives) != 2 {
		t.Fatal("backend-derived consumption receipt missing", err)
	}
	original := inst.runtimeAdmissionReceipt.Clone()
	receipt.ArtifactConsumption.Drives[0].Source.StorageKey = "caller-mutated.ext4"
	if !inst.runtimeAdmissionReceipt.Equal(original) {
		t.Fatal("caller mutated the manager's retained receipt")
	}
}

func TestNativeArtifactConsumptionRefusesWrongEvidenceAndDestroysBoot(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*RuntimeDriveHandoffObservation)
	}{
		{"instance", func(o *RuntimeDriveHandoffObservation) { o.InstanceID = idLive }},
		{"lease", func(o *RuntimeDriveHandoffObservation) { o.LeaseUID++ }},
		{"missing", func(o *RuntimeDriveHandoffObservation) { o.Drives = o.Drives[:1] }},
		{"writable base", func(o *RuntimeDriveHandoffObservation) { o.Drives[0].ReadOnly = false }},
		{"different producer", func(o *RuntimeDriveHandoffObservation) {
			o.Drives[1].Producer.Digest = "sha256:" + strings.Repeat("0", 64)
		}},
		{"different source", func(o *RuntimeDriveHandoffObservation) { o.Drives[1].Source.StorageKey = "another.ext4" }},
		{"missing process", func(o *RuntimeDriveHandoffObservation) { o.ProcessPID = 0 }},
		{"missing config", func(o *RuntimeDriveHandoffObservation) { o.ConfigHash = "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, v, request := consumedRuntimeFixture(t)
			v.edit = test.edit
			inst, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
			if err == nil || inst != nil || receipt.Binding.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 || v.observed != 1 {
				t.Fatal("invalid backend consumption retained a runtime or receipt", err)
			}
		})
	}
}

func TestNativeArtifactConsumptionRefusesMissingCapabilityOrChangedGrantBeforeAllocation(t *testing.T) {
	for _, capability := range []bool{false, true} {
		m, v, request := consumedRuntimeFixture(t)
		if capability {
			request.Binding.ArtifactSourcesHash = strings.Repeat("0", 64)
		} else {
			m.vmm = &v.runtimeSourceCapableVMM
		}
		_, receipt, err := m.WakeAdmitted(t.Context(), request, nil)
		want := runtimeadmission.ErrUnavailable
		if capability {
			want = runtimeadmission.ErrInvalid
		}
		if !errors.Is(err, want) || receipt.Binding.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 || v.verifiedCalls != 0 || v.observed != 0 {
			t.Fatal("unsupported or mismatched artifact grant allocated resources", err)
		}
	}
}

func TestNativeArtifactConsumptionDestroyJoinsObservationWithoutReceipt(t *testing.T) {
	m, v, request := consumedRuntimeFixture(t)
	entered := make(chan struct{})
	v.observe = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		inst    *Instance
		receipt runtimeadmission.Receipt
		err     error
	}
	done := make(chan result, 1)
	go func() {
		inst, receipt, err := m.WakeAdmitted(ctx, request, nil)
		done <- result{inst, receipt, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("boot did not reach native observation")
	}
	if err := m.Destroy(ctx, request.Request.Instance); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err == nil || r.inst != nil || r.receipt.Binding.Token != "" || !r.receipt.ArtifactConsumption.IsZero() || m.LiveCount() != 0 || m.LeasedCount() != 0 {
			t.Fatalf("destroy retained a measured runtime or receipt: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal("destroy failed to join native observation")
	}
}
