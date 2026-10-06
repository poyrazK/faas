package fcvm

// adr: 595 Manager lifecycle tests use simulated native measurements.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func measuredPromotionFixture(t *testing.T) (*Manager, *consumedSnapshotTestVMM, runtimeadmission.Promotion) {
	t.Helper()
	m, v, req := admittedSnapshotFixture(t, true)
	v.allowPaused, req.Request.KeepPaused = true, true
	var err error
	req.NativeInputHash, err = NativeWakeInputHash(req.Request)
	if err != nil {
		t.Fatal(err)
	}
	inst, parent, err := m.WakeAdmitted(t.Context(), req, nil)
	if err != nil || !inst.Paused || parent.Check(req.Binding, time.Now()) != nil {
		t.Fatal("complete paused load refused", err)
	}
	p := runtimeadmission.Promotion{Parent: parent, Binding: parent.Binding}
	p.Binding.Token, p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = uuid.NewString(), time.Now().UnixNano(), time.Now().Add(time.Minute).UnixNano()
	p.Binding.PayloadHash, err = runtimeadmission.HashPromotionPayload(p.ToProto())
	if err != nil {
		t.Fatal(err)
	}
	return m, v, p
}

func TestNativeMeasuredSnapshotPromotionPublishesOriginalLoadAndResume(t *testing.T) {
	m, v, p := measuredPromotionFixture(t)
	inst, r, err := m.PromoteAdmitted(t.Context(), p)
	if err != nil || inst.Paused || !inst.runtimeAdmissionReceipt.Equal(r) || p.CheckReceipt(r, time.Now()) != nil || v.resumeCalls != 1 || len(v.resumed) != 0 || len(v.resumeHookCalls) != 0 {
		t.Fatal("measured promotion did not retain exact owned lineage", err)
	}
	if !r.ArtifactConsumption.Equal(p.Parent.ArtifactConsumption) || r.SnapshotConsumption != p.Parent.SnapshotConsumption || r.SnapshotResumeEvidence.IsZero() || runtimeadmission.CheckSnapshotParent(r) != nil {
		t.Fatal("promotion rewrote paused load history")
	}
	if _, _, err := m.PromoteAdmitted(t.Context(), p); err == nil || v.resumeCalls != 1 {
		t.Fatal("measured promotion replay resumed again")
	}
}

func TestNativeMeasuredSnapshotPromotionRefusesProofAndRetiresOwner(t *testing.T) {
	for _, fault := range []string{"missing", "binding", "parent", "command", "process", "mapping", "expired", "hook"} {
		t.Run(fault, func(t *testing.T) {
			m, v, p := measuredPromotionFixture(t)
			if fault == "hook" {
				v.resumeErr = errors.New("simulated guest hook refusal")
			}
			v.editResume = func(o *RuntimeSnapshotResumeObservation) {
				switch fault {
				case "missing":
					o.ResumeEvidence = runtimeadmission.SnapshotResumeEvidence{}
				case "binding":
					o.ResumeEvidence.Binding.Token = uuid.NewString()
				case "parent":
					o.ResumeEvidence.ParentBinding.Token = uuid.NewString()
				case "command":
					o.ResumeEvidence.ResumeCommandHash = ""
				case "process":
					o.ArtifactConsumption.ProcessStart += "1"
				case "mapping":
					o.SnapshotConsumption.MappedMemoryBytes--
				case "expired":
					o.ResumeEvidence.CompletedAtUnixNano = p.Binding.ExpiresAtUnixNano
				}
			}
			inst, r, err := m.PromoteAdmitted(t.Context(), p)
			if err == nil || inst != nil || r.Binding.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 || v.resumeCalls != 1 || len(v.resumed) != 0 {
				t.Fatal("bad measured proof published or leaked", err)
			}
		})
	}
}

func TestNativeMeasuredSnapshotPromotionCapabilityRefusalPreservesPausedOwner(t *testing.T) {
	m, v, p := measuredPromotionFixture(t)
	v.version = 0
	if _, _, err := m.PromoteAdmitted(t.Context(), p); !errors.Is(err, runtimeadmission.ErrUnavailable) || v.resumeCalls != 0 || m.LiveCount() != 1 || !m.live[p.Binding.InstanceID].Paused {
		t.Fatal("unadvertised promotion changed owner", err)
	}
}

func TestNativeMeasuredSnapshotPromotionDestroyJoinsAndRefusesLateProof(t *testing.T) {
	m, v, p := measuredPromotionFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	v.observeSnapshot = func(context.Context) error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() {
		_, r, err := m.PromoteAdmitted(t.Context(), p)
		if r.Binding.Token != "" {
			err = errors.New("cancelled measured promotion produced authority")
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("measured resume did not enter")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- m.Destroy(t.Context(), p.Binding.InstanceID) }()
	m.mu.Lock()
	flight := m.runtimeAdmissionFlights[p.Binding.InstanceID]
	m.mu.Unlock()
	select {
	case <-flight.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("destroy did not cancel measured resume")
	}
	select {
	case err := <-stopped:
		t.Fatal("destroy did not join measured flight", err)
	default:
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("late measured proof published")
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("cancelled measured resume leaked its owner")
	}
}
