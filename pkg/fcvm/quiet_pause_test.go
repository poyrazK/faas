// adr: 1151 — vmmd pauses an eligible instance between requests and resumes it
// before the next one reaches the guest; a racing request never meets a
// paused guest.
package fcvm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeQuietActivity struct {
	mu       sync.Mutex
	inflight map[string]int64
	since    map[string]time.Time
}

func newFakeQuietActivity() *fakeQuietActivity {
	return &fakeQuietActivity{inflight: map[string]int64{}, since: map[string]time.Time{}}
}

func (a *fakeQuietActivity) Quiet(id string) (int64, time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	since, ok := a.since[id]
	return a.inflight[id], since, ok
}

func (a *fakeQuietActivity) set(id string, inflight int64, since time.Time) {
	a.mu.Lock()
	a.inflight[id], a.since[id] = inflight, since
	a.mu.Unlock()
}

type pausingVMM struct {
	*fakeVMM
	mu       sync.Mutex
	paused   []string
	pauseErr error
	// duringPause runs inside PauseVM, before it returns.
	duringPause func()
}

func (v *pausingVMM) PauseVM(_ context.Context, l Lease) error {
	if v.duringPause != nil {
		v.duringPause()
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.paused = append(v.paused, l.Instance)
	return v.pauseErr
}

func (v *pausingVMM) pauses() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.paused)
}

func (v *pausingVMM) resumes() int {
	v.fakeVMM.mu.Lock()
	defer v.fakeVMM.mu.Unlock()
	return len(v.fakeVMM.resumed)
}

func quietWake(instance string) WakeRequest {
	w := admissionWake(instance)
	w.QuietPauseAfter = 2 * time.Second
	return w
}

// newQuietManager wakes instance "q" with quiet pause on, as if the pause
// pass first saw it an hour ago.
func newQuietManager(t *testing.T, v *pausingVMM, act QuietActivity) *Manager {
	t.Helper()
	m := newTestManager(&fakeRunner{}, v).WithQuietPause(QuietPauseConfig{Activity: act})
	if _, err := m.Wake(t.Context(), quietWake("q")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "q") })
	seenQuiet(m, "q", time.Now().Add(-time.Hour))
	return m
}

// seenQuiet records when the pause pass first saw the live incarnation of id.
func seenQuiet(m *Manager, id string, at time.Time) *quietState {
	m.mu.Lock()
	inst := m.live[id]
	m.mu.Unlock()
	return m.quietStateFor(id, inst, at)
}

func TestQuietPauseAfterFor(t *testing.T) {
	base := quietWake("q")
	for _, tc := range []struct {
		name          string
		edit          func(*WakeRequest)
		qualification bool
		want          time.Duration
	}{
		{name: "function instance", edit: func(*WakeRequest) {}, want: 2 * time.Second},
		{name: "not requested", edit: func(w *WakeRequest) { w.QuietPauseAfter = 0 }},
		{name: "private network peer", edit: func(w *WakeRequest) { w.PrivateNetworkID = "net-1" }},
		{name: "warm pool entry", edit: func(w *WakeRequest) { w.KeepPaused = true }},
		{name: "execution", edit: func(w *WakeRequest) { w.ExecutionOnly = true }},
		{name: "app task", edit: func(w *WakeRequest) { w.AppTaskOnly = true }},
		{name: "builder", edit: func(w *WakeRequest) { w.ExportDir = "/export" }},
		{name: "qualification", edit: func(*WakeRequest) {}, qualification: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := base
			tc.edit(&w)
			if got := quietPauseAfterFor(w, tc.qualification); got != tc.want {
				t.Fatalf("quietPauseAfterFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestQuietPausePass(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name      string
		inflight  int64
		since     time.Duration // before now; 0 means activity never seen
		tail      int
		flight    bool
		wantPause bool
	}{
		{name: "idle past the delay", since: 3 * time.Second, wantPause: true},
		{name: "last request ended recently", since: time.Second},
		{name: "request in flight", inflight: 1, since: time.Minute},
		{name: "tail task running", since: time.Minute, tail: 1},
		{name: "operation in progress", since: time.Minute, flight: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			act := newFakeQuietActivity()
			v := &pausingVMM{fakeVMM: &fakeVMM{}}
			m := newQuietManager(t, v, act)
			if tc.since > 0 || tc.inflight > 0 {
				act.set("q", tc.inflight, now.Add(-tc.since))
			}
			m.mu.Lock()
			m.live["q"].TailCount = tc.tail
			if tc.flight {
				m.instanceFlights["q"] = &instanceFlight{done: make(chan struct{})}
			}
			m.mu.Unlock()
			m.quietPausePass(t.Context(), now)
			if tc.flight {
				m.mu.Lock()
				delete(m.instanceFlights, "q")
				m.mu.Unlock()
			}
			paused, _ := m.QuietPauseState("q")
			if paused != tc.wantPause || (v.pauses() == 1) != tc.wantPause {
				t.Fatalf("paused=%v pauses=%d, want paused=%v", paused, v.pauses(), tc.wantPause)
			}
		})
	}
}

func TestQuietPauseWaitsForFirstSighting(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	m.quietMu.Lock()
	m.quietStates = nil
	m.quietMu.Unlock()
	now := time.Now()
	m.quietPausePass(t.Context(), now) // first sighting, no request yet
	m.quietPausePass(t.Context(), now.Add(time.Second))
	if v.pauses() != 0 {
		t.Fatalf("paused %d times within the delay of the first sighting", v.pauses())
	}
	m.quietPausePass(t.Context(), now.Add(2*time.Second))
	if v.pauses() != 1 {
		t.Fatalf("pauses = %d after the delay, want 1", v.pauses())
	}
}

func TestQuietResume(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	if err := m.QuietResume(t.Context(), "q"); err != nil || v.resumes() != 0 {
		t.Fatalf("QuietResume on a running instance: err=%v resumes=%d", err, v.resumes())
	}
	_, epoch0 := m.QuietGate("q")
	m.quietPausePass(t.Context(), time.Now())
	paused, epoch1 := m.QuietGate("q")
	if !paused || epoch1 != epoch0+1 {
		t.Fatalf("after pause: paused=%v epoch %d -> %d", paused, epoch0, epoch1)
	}
	if p, since := m.QuietPauseState("q"); !p || since.IsZero() {
		t.Fatalf("QuietPauseState = %v, %v", p, since)
	}
	if err := m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	paused, epoch2 := m.QuietGate("q")
	if paused || epoch2 != epoch1+1 || v.resumes() != 1 {
		t.Fatalf("after resume: paused=%v epoch=%d resumes=%d", paused, epoch2, v.resumes())
	}
	if err := m.QuietResume(t.Context(), "q"); err != nil || v.resumes() != 1 {
		t.Fatalf("second QuietResume: err=%v resumes=%d", err, v.resumes())
	}
}

func TestQuietResumeFailureKeepsInstancePaused(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	boom := errors.New("boom")
	v.fakeVMM.mu.Lock()
	v.fakeVMM.resumeErr = boom
	v.fakeVMM.mu.Unlock()
	if err := m.QuietResume(t.Context(), "q"); !errors.Is(err, boom) {
		t.Fatalf("QuietResume err = %v, want %v", err, boom)
	}
	if paused, _ := m.QuietPauseState("q"); !paused {
		t.Fatal("a failed resume must leave the instance marked paused so the next request retries")
	}
	v.fakeVMM.mu.Lock()
	v.fakeVMM.resumeErr = nil
	v.fakeVMM.mu.Unlock()
	if err := m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
}

func TestQuietPauseFailureKeepsInstanceRunning(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}, pauseErr: errors.New("boom")}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	if paused, _ := m.QuietPauseState("q"); paused {
		t.Fatal("instance marked paused after PauseVM failed")
	}
	if err := m.QuietResume(t.Context(), "q"); err != nil || v.resumes() != 0 {
		t.Fatalf("QuietResume after a failed pause: err=%v resumes=%d", err, v.resumes())
	}
}

// A request that arrives while the Firecracker pause call is in flight must
// wait for it and then resume the guest, never reach it paused.
func TestQuietResumeWaitsForRacingPause(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	resumed := make(chan error, 1)
	v.duringPause = func() {
		act.set("q", 1, time.Now()) // the request marks activity...
		go func() { resumed <- m.QuietResume(context.Background(), "q") }()
		time.Sleep(20 * time.Millisecond) // ...and blocks on the instance lock
	}
	m.quietPausePass(t.Context(), time.Now())
	select {
	case err := <-resumed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("QuietResume did not return")
	}
	if paused, _ := m.QuietPauseState("q"); paused || v.pauses() != 1 || v.resumes() != 1 {
		t.Fatalf("paused=%v pauses=%d resumes=%d, want a pause then a resume", paused, v.pauses(), v.resumes())
	}
}

// A request that marks activity before the pause's final check stops the
// pause altogether.
func TestQuietPauseBacksOffForRequestBeforeFinalCheck(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	now := time.Now()
	act.set("q", 0, now.Add(-time.Minute))
	st := seenQuiet(m, "q", now.Add(-time.Hour))
	// The pass reads activity, then takes st.mu. Hold st.mu so the request
	// lands between the two.
	st.mu.Lock()
	done := make(chan struct{})
	go func() {
		m.quietPausePass(context.Background(), now)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	act.set("q", 1, now)
	st.mu.Unlock()
	<-done
	if v.pauses() != 0 {
		t.Fatalf("paused %d times after a request began", v.pauses())
	}
}

func TestLiveOperationResumesQuietPausedInstance(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	ctx, inst, flight, err := m.beginLiveInstanceFlight(t.Context(), "q")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishInstanceFlight("q", flight)
	if v.pauses() != 1 {
		t.Fatalf("instance was not quiet-paused before the operation: pauses=%d", v.pauses())
	}
	if paused, _ := m.QuietPauseState("q"); paused || v.resumes() != 1 || inst == nil || ctx == nil {
		t.Fatalf("operation started on a quiet-paused guest: paused=%v resumes=%d", paused, v.resumes())
	}
	// With the operation registered, the pass leaves the instance alone.
	m.quietPausePass(t.Context(), time.Now())
	if v.pauses() != 1 {
		t.Fatalf("paused during a live operation: pauses=%d", v.pauses())
	}
}

func TestQuietPausePassForgetsDestroyedInstances(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	if err := m.Destroy(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	m.quietPausePass(t.Context(), time.Now())
	if st := m.quietStateIfAny("q"); st != nil {
		t.Fatal("quiet state kept after the instance was destroyed")
	}
	if err := m.QuietResume(t.Context(), "q"); err != nil {
		t.Fatalf("QuietResume of a destroyed instance: %v", err)
	}
}

func TestQuietPauseDisabledWithoutActivity(t *testing.T) {
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newTestManager(&fakeRunner{}, v)
	if _, err := m.Wake(t.Context(), quietWake("q")); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Destroy(context.Background(), "q") }()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	m.RunQuietPause(ctx) // returns at once without WithQuietPause
	if v.pauses() != 0 {
		t.Fatalf("paused %d times with quiet pause off", v.pauses())
	}
}

// schedd wakes a parked instance again under the same id. The new VM must not
// inherit the old one's pause state, nor be measured idle from the old VM's
// last request.
func TestQuietStateResetsForNewIncarnation(t *testing.T) {
	act := newFakeQuietActivity()
	v := &pausingVMM{fakeVMM: &fakeVMM{}}
	m := newQuietManager(t, v, act)
	act.set("q", 0, time.Now().Add(-time.Minute))
	m.quietPausePass(t.Context(), time.Now())
	if paused, _ := m.QuietPauseState("q"); !paused {
		t.Fatal("first incarnation was not paused")
	}
	if err := m.Destroy(t.Context(), "q"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Wake(t.Context(), quietWake("q")); err != nil {
		t.Fatal(err)
	}
	if paused, _ := m.QuietPauseState("q"); paused {
		t.Fatal("new incarnation reported the old one's pause")
	}
	if err := m.QuietResume(t.Context(), "q"); err != nil || v.resumes() != 0 {
		t.Fatalf("QuietResume resumed a VM that was never paused: err=%v resumes=%d", err, v.resumes())
	}
	now := time.Now()
	m.quietPausePass(t.Context(), now) // first sighting of the new VM
	m.quietPausePass(t.Context(), now.Add(time.Second))
	if v.pauses() != 1 {
		t.Fatalf("new incarnation paused within its first delay: pauses=%d", v.pauses())
	}
	m.quietPausePass(t.Context(), now.Add(2*time.Second))
	if v.pauses() != 2 {
		t.Fatalf("new incarnation not paused after its delay: pauses=%d", v.pauses())
	}
}
