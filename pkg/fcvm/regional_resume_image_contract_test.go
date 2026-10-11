package fcvm

// adr: 791
import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/healthcheckproto"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestServingResumeChallengesImageAfterNativeResumeAndHook(t *testing.T) {
	for _, outcome := range []string{"fresh", "unhealthy", "stale-reply"} {
		t.Run(outcome, func(t *testing.T) {
			var native, hooks atomic.Int32
			var mu sync.Mutex
			var firstNonce string
			v, lease := imageHealthcheckAndResumeTestSocket(t, func(kind uint32, req healthcheckproto.Request) (uint32, any) {
				if native.Load() == 0 || hooks.Load() != native.Load() {
					t.Error("command challenge preceded native resume and hook")
				}
				if kind == healthcheckproto.ConfigProbe {
					return healthcheckproto.ConfigAck, healthcheckproto.Config{Nonce: req.Nonce, RuntimeID: "main", IntervalNS: int64(time.Second), TimeoutNS: int64(time.Second), Retries: 3}
				}
				mu.Lock()
				defer mu.Unlock()
				if firstNonce == "" {
					firstNonce = req.Nonce
				}
				nonce := req.Nonce
				if outcome == "stale-reply" {
					nonce = firstNonce
				}
				return healthcheckproto.Ack, healthcheckproto.Response{Nonce: nonce, RuntimeID: "main", Healthy: outcome != "unhealthy"}
			}, func() { hooks.Add(1) })
			v.clients = map[string]*http.Client{lease.Instance: {Transport: servingResumeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodPatch || r.URL.Path != "/vm" {
					t.Error("unexpected native resume operation")
				}
				native.Add(1)
				return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
			})}}
			spec := ServingResumeSpec{ImageHealthcheckRequired: true}
			err := v.ResumeServingVM(t.Context(), lease, spec)
			if (err == nil) != (outcome != "unhealthy") {
				t.Fatal("image gate ignored fresh outcome", err)
			}
			if outcome == "stale-reply" {
				if err := v.ResumeServingVM(t.Context(), lease, spec); err == nil {
					t.Fatal("second serving resume reused first challenge result")
				}
			}
			if hooks.Load() != native.Load() {
				t.Fatal("image resume skipped fresh entropy and clock hook")
			}
		})
	}
}

type checkedRegionalServingVMM struct {
	*fakeVMM
	specs []ServingResumeSpec
	fail  error
}

func (v *checkedRegionalServingVMM) ResumeServingVM(ctx context.Context, lease Lease, spec ServingResumeSpec) error {
	v.specs = append(v.specs, spec)
	if !spec.ImageHealthcheckRequired {
		return errors.New("stored image contract missing")
	}
	if v.fail != nil {
		return v.fail
	}
	return v.fakeVMM.ResumeServingVM(ctx, lease, spec)
}

func TestRegionalServingResumeRetainsImageContractAndHoldAfterFailure(t *testing.T) {
	v := &checkedRegionalServingVMM{fakeVMM: &fakeVMM{}}
	m, _, req := regionalHostFixture(t, v)
	req.KeepPaused, req.Snapshot, req.ImageHealthcheckRequired = true, usableSnapshot(), true
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityWarm
	req.RegionalRuntime.Seat = nil
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.WithoutCancel(t.Context()), req.Instance) })
	p := req.RegionalRuntime.Clone()
	p.Seat = &runtimequota.ServingSeat{Permit: p.Permit, ID: uuid.NewString()}
	v.fail = errors.New("fresh image command unhealthy")
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); !errors.Is(err, v.fail) {
		t.Fatal("failed image check acknowledged serving", err)
	}
	if _, ok := m.NetnsFor(req.Instance); ok || !inst.Paused || !admissionForInstance(inst).RegionalResumePending {
		t.Fatal("failed image check dropped pending hold or published serving")
	}
	v.fail = nil
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err != nil {
		t.Fatal("same-seat checked retry failed", err)
	}
	if len(v.specs) != 2 || !v.specs[0].ImageHealthcheckRequired || !v.specs[1].ImageHealthcheckRequired {
		t.Fatal("retry lost stored image requirement")
	}
}
