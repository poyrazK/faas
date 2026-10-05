package fcvm

// adr: 624
import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
	"github.com/onebox-faas/faas/pkg/runtimequotahost"
)

// Compact DB keys remain the actual runtime keys. Canonical protocol UUIDs
// identify the same physical attempt, never a second native launch.
func TestRegionalCompactResidentIdentityKeepsOnePhysicalAttempt(t *testing.T) {
	v := &fakeVMM{}
	m, _, req := regionalHostFixture(t, v)
	req.KeepPaused, req.Snapshot = true, usableSnapshot()
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityWarm
	req.RegionalRuntime.Seat = nil
	canonical := req.Instance
	for _, id := range []*string{&req.Instance, &req.AppID, &req.AccountID, &req.DeploymentID} {
		*id = strings.ReplaceAll(*id, "-", "")
	}
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	p := req.RegionalRuntime.Clone()
	p.Seat = &runtimequota.ServingSeat{Permit: p.Permit, ID: uuid.NewString()}
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err != nil {
		t.Fatal("compact resident key did not resume", err)
	}
	alias := req
	alias.Instance = canonical
	if _, err := m.Wake(t.Context(), alias); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatal("UUID encoding minted a second physical attempt", err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}

func TestRegionalResidentResumeInstallsFreshSeat(t *testing.T) {
	v := &fakeVMM{}
	m, j, req := regionalHostFixture(t, v)
	req.KeepPaused, req.Snapshot = true, usableSnapshot()
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityWarm
	req.RegionalRuntime.Seat = nil
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.NetnsFor(req.Instance); ok {
		t.Fatal("paused warm restore exposed serving network")
	}
	p := req.RegionalRuntime.Clone()
	p.Seat = &runtimequota.ServingSeat{Permit: p.Permit, ID: uuid.NewString()}
	if err := m.ResumeVM(t.Context(), req.Instance); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatal("omitted serving placement resumed a regional resident", err)
	}
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err != nil {
		t.Fatal(err)
	}
	if inst.Paused || admissionForInstance(inst).RegionalResumePending || len(v.resumed) != 1 {
		t.Fatal("successful resume did not publish running bookkeeping")
	}
	if _, err := j.FenceSeat(*p.Seat, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	next := p.Clone()
	next.Seat.ID = uuid.NewString()
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, next); err != nil {
		t.Fatal(err)
	}
	current := admissionForInstance(inst).RegionalRuntime
	next.Seat.ID = uuid.NewString()
	if *admissionForInstance(inst).RegionalRuntime.Seat != *current.Seat || current.Permit != p.Permit {
		t.Fatal("caller mutation replaced immutable host pins")
	}
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); !errors.Is(err, runtimequotahost.ErrFenced) {
		t.Fatal("stale retry restored the old seat", err)
	}
	if err := m.Destroy(t.Context(), req.Instance); err != nil {
		t.Fatal(err)
	}
}

type blockedRegionalResumeVMM struct {
	*fakeVMM
	once             sync.Once
	entered, release chan struct{}
}

func (v *blockedRegionalResumeVMM) ResumeServingVM(ctx context.Context, lease Lease, spec ServingResumeSpec) error {
	v.once.Do(func() { close(v.entered) })
	<-v.release
	return v.fakeVMM.ResumeServingVM(ctx, lease, spec)
}

func (v *fakeVMM) ResumeServingVM(ctx context.Context, lease Lease, spec ServingResumeSpec) error {
	if err := v.ResumeVM(ctx, lease); err != nil {
		return err
	}
	if spec.AfterPausedRestore {
		return v.TriggerResumeHook(ctx, lease, time.Now().UnixNano())
	}
	return nil
}

type capturingServingResumeVMM struct {
	*fakeVMM
	specs []ServingResumeSpec
}

func (v *capturingServingResumeVMM) ResumeServingVM(ctx context.Context, lease Lease, spec ServingResumeSpec) error {
	v.specs = append(v.specs, spec)
	return v.fakeVMM.ResumeServingVM(ctx, lease, spec)
}

func TestRegionalWarmResumeCompletesDeferredRestoreBeforeServing(t *testing.T) {
	v := &capturingServingResumeVMM{fakeVMM: &fakeVMM{}}
	m, _, req := regionalHostFixture(t, v)
	req.KeepPaused, req.Snapshot = true, usableSnapshot()
	req.RegionalRuntime.Permit.Kind = runtimequota.CapacityWarm
	req.RegionalRuntime.Seat = nil
	req.HealthcheckGRPC, req.HealthcheckGRPCService, req.StartupDeadlineS = true, "regional-app", 17
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	p := req.RegionalRuntime.Clone()
	p.Seat = &runtimequota.ServingSeat{Permit: p.Permit, ID: uuid.NewString()}
	failure := errors.New("guest restore hook failed")
	v.resumeHookErr = failure
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); !errors.Is(err, failure) {
		t.Fatal("hook failure acknowledged", err)
	}
	if _, ok := m.NetnsFor(req.Instance); ok || !inst.Paused {
		t.Fatal("failed restore completion published serving")
	}
	v.resumeHookErr = nil
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err != nil {
		t.Fatal("exact-seat restore retry failed", err)
	}
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err != nil {
		t.Fatal("known completed resume retry failed", err)
	}
	for i, spec := range v.specs {
		if spec.AfterPausedRestore != (i < 2) || !spec.HealthcheckGRPC || spec.HealthcheckGRPCService != req.HealthcheckGRPCService || spec.StartupDeadlineS != 17 {
			t.Fatal("retained restore readiness lost", i, spec)
		}
	}
	if len(v.specs) != 3 || len(v.resumeHookCalls) != 2 {
		t.Fatal("failed hook was skipped or completed hook repeated", v.specs, v.resumeHookCalls)
	}
	_ = m.Destroy(t.Context(), req.Instance)
}

func TestRegionalResumeRequiresServingCompletionCapability(t *testing.T) {
	v := &fakeVMM{}
	m, _, req := regionalHostFixture(t, struct{ VMM }{v})
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, req.RegionalRuntime.Clone()); !errors.Is(err, runtimequotahost.ErrBinding) {
		t.Fatal("plain unpause backend acknowledged serving", err)
	}
	if len(v.resumed) != 0 || admissionForInstance(inst).RegionalResumePending {
		t.Fatal("unsupported backend changed resident")
	}
	_ = m.Destroy(t.Context(), req.Instance)
}

func TestIndependentWarmResumeCompletesDeferredRestore(t *testing.T) {
	v := &fakeVMM{}
	m := newTestManager(&fakeRunner{}, v)
	req := wakeReq("independent-warm", usableSnapshot())
	req.KeepPaused, req.Snapshot = true, usableSnapshot()
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("deferred hook failed")
	v.resumeHookErr = failure
	if err := m.ResumeVM(t.Context(), req.Instance); !errors.Is(err, failure) {
		t.Fatal("independent warm hook failure acknowledged", err)
	}
	if _, ok := m.NetnsFor(req.Instance); ok || !inst.Paused {
		t.Fatal("failed independent warm resume became serving")
	}
	v.resumeHookErr = nil
	for range 2 {
		if err := m.ResumeVM(t.Context(), req.Instance); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := m.NetnsFor(req.Instance); !ok || inst.Paused || len(v.resumeHookCalls) != 2 {
		t.Fatal("independent warm retry lost deferred completion or repeated completed hook")
	}
	_ = m.Destroy(t.Context(), req.Instance)
}

func TestIndependentWarmResumeExcludesConcurrentRestoreCompletion(t *testing.T) {
	v := &blockedRegionalResumeVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), release: make(chan struct{})}
	m := newTestManager(&fakeRunner{}, v)
	req := wakeReq("independent-warm-exclusive", usableSnapshot())
	req.KeepPaused = true
	if _, err := m.Wake(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- m.ResumeVM(t.Context(), req.Instance) }()
	select {
	case <-v.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("deferred resume did not reach native boundary")
	}
	if err := m.ResumeVM(t.Context(), req.Instance); err == nil {
		t.Fatal("concurrent restore completion repeated guest hook", err)
	}
	close(v.release)
	if err := <-done; err != nil || len(v.resumeHookCalls) != 1 {
		t.Fatal("exclusive completion failed", err)
	}
	_ = m.Destroy(t.Context(), req.Instance)
}

func TestRegionalResumeRetainsSeatThroughUncertainNativeResult(t *testing.T) {
	for _, result := range []string{"success", "native-error", "physical-fence", "seat-fence", "owner-fence", "cancelled"} {
		t.Run(result, func(t *testing.T) {
			v := &blockedRegionalResumeVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), release: make(chan struct{})}
			m, j, req := regionalHostFixture(t, v)
			inst, err := m.Wake(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			original := req.RegionalRuntime.Clone()
			if _, err := j.FenceSeat(*original.Seat, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			p := original.Clone()
			p.Seat.ID = uuid.NewString()
			failure := errors.New("uncertain resume result")
			if result == "native-error" {
				v.resumeErr = failure
			}
			resumeCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- m.ResumeRegionalRuntime(resumeCtx, req.Instance, p) }()
			select {
			case <-v.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("resume did not reach native boundary")
			}
			if _, ok := m.NetnsFor(req.Instance); ok {
				t.Fatal("pending resume exposed a serving network")
			}
			for range 10 {
				admission := admissionForInstance(inst)
				if !admission.RegionalResumePending || *admission.RegionalRuntime.Seat != *p.Seat {
					t.Fatal("pending resume lost the new seat")
				}
			}
			switch result {
			case "physical-fence":
				_, err = j.FencePermit(p.Permit, uuid.NewString())
			case "seat-fence":
				_, err = j.FenceSeat(*p.Seat, uuid.NewString())
			case "cancelled":
				cancel()
			case "owner-fence":
				// Startup wiring is immutable in production. This synchronized
				// seam changes only the post-native check in this fixture.
				m.WithRuntimeAuthorizer(func(context.Context, RuntimeAdmission) error { return failure })
			}
			if err != nil {
				t.Fatal(err)
			}
			close(v.release)
			resumeErr := <-done
			if result == "cancelled" && !errors.Is(resumeErr, context.Canceled) {
				t.Fatal("cancelled native result published", resumeErr)
			}
			if (resumeErr == nil) != (result == "success") {
				t.Fatal("native/post-admission result", resumeErr)
			}
			current := admissionForInstance(inst)
			if *current.RegionalRuntime.Seat != *p.Seat || current.RegionalRuntime.Permit != original.Permit || current.RegionalResumePending != (result != "success") {
				t.Fatal("failed resume rolled back or published serving authority")
			}
			if _, ok := m.NetnsFor(req.Instance); ok != (result == "success") {
				t.Fatal("uncertain resume remained routable")
			}
			if result == "native-error" {
				v.resumeErr = nil
				if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err != nil {
					t.Fatal("exact-seat retry failed", err)
				}
			}
			if err := m.Destroy(t.Context(), req.Instance); err != nil {
				t.Fatal(err)
			}
			// Mock cleanup and operation counts do not prove native stop.
		})
	}
}

func TestRegionalResumeCannotRebindPhysicalAttempt(t *testing.T) {
	for _, pin := range []string{"permit", "instance", "run", "node", "epoch", "deployment", "grant", "missing-seat"} {
		t.Run(pin, func(t *testing.T) {
			v := &fakeVMM{}
			m, _, req := regionalHostFixture(t, v)
			if _, err := m.Wake(t.Context(), req); err != nil {
				t.Fatal(err)
			}
			p := req.RegionalRuntime.Clone()
			switch pin {
			case "permit":
				p.Permit.ID = uuid.NewString()
			case "instance":
				p.Permit.InstanceID = uuid.NewString()
			case "run":
				p.Permit.RunID = uuid.NewString()
			case "node":
				p.Permit.NodeID = uuid.NewString()
			case "epoch":
				p.Permit.HostEpochID = uuid.NewString()
			case "deployment":
				p.Permit.DeploymentID = uuid.NewString()
			case "grant":
				p.Permit.Grant.AllocationID = uuid.NewString()
			case "missing-seat":
				p.Seat = nil
			}
			if p.Seat != nil {
				p.Seat.Permit = p.Permit
			}
			if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err == nil || len(v.resumed) != 0 {
				t.Fatal("resume rebound a retained physical attempt", err)
			}
			_ = m.Destroy(t.Context(), req.Instance)
		})
	}
}

// Cancellation can arrive while the native backend is still running. A stop
// must join that operation before touching the retained physical identity.
type teardownJoinedRegionalResumeVMM struct {
	*fakeVMM
	entered, cancelled, release, stopping chan struct{}
}

func (v *teardownJoinedRegionalResumeVMM) ResumeServingVM(ctx context.Context, _ Lease, _ ServingResumeSpec) error {
	close(v.entered)
	<-ctx.Done()
	close(v.cancelled)
	<-v.release
	return nil // Model a late successful native result after cancellation.
}

func (v *teardownJoinedRegionalResumeVMM) DestroyWithExport(ctx context.Context, lease Lease, exportDir string) (int, error) {
	close(v.stopping)
	return v.fakeVMM.DestroyWithExport(ctx, lease, exportDir)
}

func TestRegionalResumeDestroyJoinsNativeCompletion(t *testing.T) {
	v := &teardownJoinedRegionalResumeVMM{fakeVMM: &fakeVMM{}, entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), stopping: make(chan struct{})}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(v.release) }) }
	t.Cleanup(unblock)
	m, _, req := regionalHostFixture(t, v)
	req.KeepPaused, req.Snapshot = true, usableSnapshot()
	req.RegionalRuntime.Permit.Kind, req.RegionalRuntime.Seat = runtimequota.CapacityWarm, nil
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	p := req.RegionalRuntime.Clone()
	p.Seat = &runtimequota.ServingSeat{Permit: p.Permit, ID: uuid.NewString()}
	resumed := make(chan error, 1)
	go func() { resumed <- m.ResumeRegionalRuntime(t.Context(), req.Instance, p) }()
	select {
	case <-v.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not reach native boundary")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- m.Destroy(t.Context(), req.Instance) }()
	select {
	case <-v.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("destroy did not cancel the native resume flight")
	}
	select {
	case <-v.stopping:
		t.Fatal("destroy entered native teardown before resume returned")
	default:
	}
	if _, ok := m.NetnsFor(req.Instance); ok {
		t.Fatal("cancelled pending resume remained routable")
	}
	if err := m.ResumeRegionalRuntime(t.Context(), req.Instance, p); err == nil {
		t.Fatal("new resume entered the teardown flight")
	}
	unblock()
	select {
	case err := <-resumed:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("late native success acknowledged cancelled resume", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not join")
	}
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("destroy did not join")
	}
	if !admissionForInstance(inst).RegionalResumePending {
		t.Fatal("cancelled resume cleared the serving fence")
	}
}
