// adr:683
package fcvm

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

type imageReadinessManagerVMM struct {
	*fakeVMM
	checks int
	fail   error
}

func (v *imageReadinessManagerVMM) WaitImageHealthcheck(_ context.Context, _ Lease, _ int) error {
	v.checks++
	if len(v.resumeHookCalls) == 0 {
		return errors.New("command check preceded the resume hook")
	}
	return v.fail
}

func TestImageHealthcheckManagerBootRestoreAndWarmResume(t *testing.T) {
	for _, paused := range []bool{false, true} {
		t.Run(map[bool]string{false: "cold", true: "paused restore"}[paused], func(t *testing.T) {
			vmm := &imageReadinessManagerVMM{fakeVMM: &fakeVMM{}}
			m := newTestManager(&fakeRunner{}, vmm)
			request := WakeRequest{Instance: "image-check", BaseKey: "/base", LayerKey: "/layer", VcpuCount: 2, MemSizeMiB: 256, Plan: api.PlanHobby, ImageHealthcheckRequired: true, KeepPaused: paused}
			if paused {
				request.Snapshot = usableSnapshot()
			}
			instance, err := m.Wake(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), request.Instance) })
			if !instance.ImageHealthcheckRequired || !m.ImageHealthcheckRequiredFor(request.Instance) {
				t.Fatal("instance lost its boot contract")
			}
			if paused {
				if len(vmm.restoreSpecs) != 1 || !vmm.restoreSpecs[0].ImageHealthcheckRequired {
					t.Fatal("restore lost command gate")
				}
				if vmm.checks != 0 {
					t.Fatal("paused reservation claimed command success")
				}
				if err := m.ResumeVM(t.Context(), request.Instance); err != nil {
					t.Fatal(err)
				}
				if vmm.checks != 1 {
					t.Fatal("serving resume did not execute a fresh check")
				}
				vmm.fail = errors.New("current command unhealthy")
				if err := m.ResumeVM(t.Context(), request.Instance); err == nil {
					t.Fatal("second resume reused prior success")
				}
			} else if len(vmm.coldBootSpecs) != 1 || !vmm.coldBootSpecs[0].ImageHealthcheckRequired {
				t.Fatal("cold boot lost command gate")
			}
		})
	}
}

func TestImageHealthcheckManagerCannotResumeWithoutGate(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	request := WakeRequest{Instance: "no-checker", BaseKey: "/base", LayerKey: "/layer", VcpuCount: 2, MemSizeMiB: 256, Plan: api.PlanHobby, ImageHealthcheckRequired: true}
	if _, err := m.Wake(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), request.Instance) })
	if err := m.ResumeVM(t.Context(), request.Instance); err == nil {
		t.Fatal("missing fresh-check capability authorized resume")
	}
}
