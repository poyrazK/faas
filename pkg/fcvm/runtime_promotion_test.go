// adr: 386 — fresh grants authorize promotion of the exact paused native lease.

package fcvm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func nativePromotionFixture(t *testing.T, m *Manager) runtimeadmission.Promotion {
	t.Helper()
	req := admittedFixture(t, m)
	req.Request.Snapshot, req.Request.KeepPaused = usableSnapshot(), true
	req.NativeInputHash, _ = NativeWakeInputHash(req.Request)
	_, parent, err := m.WakeAdmitted(t.Context(), req, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := runtimeadmission.Promotion{Parent: parent, Binding: parent.Binding}
	now := time.Now()
	p.Binding.Token, p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = uuid.NewString(), now.UnixNano(), now.Add(time.Minute).UnixNano()
	p.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(p.ToProto())
	return p
}

func TestNativePromotionResumesExactLeaseOnce(t *testing.T) {
	v := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, v)
	p := nativePromotionFixture(t, m)
	defer m.Destroy(t.Context(), p.Binding.InstanceID)
	if err := m.ResumeVM(t.Context(), p.Binding.InstanceID); !errors.Is(err, runtimeadmission.ErrUnavailable) {
		t.Fatalf("legacy resume bypassed admission: %v", err)
	}
	inst, r, err := m.PromoteAdmitted(t.Context(), p)
	if err != nil || inst.Paused || p.CheckReceipt(r, time.Now()) != nil || len(v.resumed) != 1 || len(v.resumeHookCalls) != 1 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatalf("promotion: %+v %v", r, err)
	}
	if _, _, err := m.PromoteAdmitted(t.Context(), p); err == nil || len(v.resumed) != 1 {
		t.Fatalf("promotion replay resumed twice: %v", err)
	}
}

func TestNativePromotionHookFailureDestroysAndProducesNoReceipt(t *testing.T) {
	v := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, v)
	p := nativePromotionFixture(t, m)
	v.resumeHookErr = errors.New("simulated resume hook refusal")
	inst, r, err := m.PromoteAdmitted(t.Context(), p)
	if err == nil || inst != nil || r.Binding.Token != "" || m.LiveCount() != 0 || m.LeasedCount() != 0 || len(v.resumed) != 1 || len(v.resumeHookCalls) != 1 {
		t.Fatalf("failed guest resume hook published/leaked: %+v %v live=%d leases=%d", r, err, m.LiveCount(), m.LeasedCount())
	}
}

func TestNativePromotionRefusesChangedLeaseAndPolicy(t *testing.T) {
	for _, change := range []string{"lease", "nonce", "policy", "token"} {
		t.Run(change, func(t *testing.T) {
			v := &fakeVMM{}
			m := newTestManager(&fakeRunner{}, v)
			p := nativePromotionFixture(t, m)
			defer m.Destroy(t.Context(), p.Binding.InstanceID)
			switch change {
			case "lease":
				p.Parent.LeaseUID++
				p.Binding.PayloadHash, _ = runtimeadmission.HashPromotionPayload(p.ToProto())
			case "nonce":
				p.Binding.Incarnation = uuid.NewString()
			case "policy":
				m.appEgressPolicies[p.Binding.AppID] = appEgressPolicy{revision: p.Binding.EgressRevision + 1}
			case "token":
				p.Binding.Token = p.Parent.Binding.Token
			}
			if _, _, err := m.PromoteAdmitted(t.Context(), p); err == nil || len(v.resumed) != 0 {
				t.Fatalf("changed authority resumed: %v", err)
			}
		})
	}
}

type blockedPromotionVMM struct {
	fakeVMM
	entered, release chan struct{}
}

func (v *blockedPromotionVMM) ResumeVM(ctx context.Context, l Lease) error {
	close(v.entered)
	<-v.release
	return v.fakeVMM.ResumeVM(ctx, l)
}

func TestNativePromotionDestroyJoinsAndRefusesLateSuccess(t *testing.T) {
	v := &blockedPromotionVMM{entered: make(chan struct{}), release: make(chan struct{})}
	m := newTestManager(&fakeRunner{}, v)
	p := nativePromotionFixture(t, m)
	done := make(chan error, 1)
	go func() {
		_, r, err := m.PromoteAdmitted(t.Context(), p)
		if r.Binding.Token != "" {
			err = errors.New("cancelled promotion produced authority")
		}
		done <- err
	}()
	select {
	case <-v.entered:
	case <-time.After(time.Second):
		t.Fatal("resume did not enter")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- m.Destroy(t.Context(), p.Binding.InstanceID) }()
	m.mu.Lock()
	flight := m.runtimeAdmissionFlights[p.Binding.InstanceID]
	m.mu.Unlock()
	select {
	case <-flight.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("destroy did not cancel resume")
	}
	select {
	case err := <-stopped:
		t.Fatalf("destroy did not join flight: %v", err)
	default:
	}
	close(v.release)
	if err := <-done; err == nil {
		t.Fatal("late native success published")
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("cancelled promotion leaked native VM")
	}
}
