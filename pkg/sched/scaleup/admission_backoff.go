package scaleup

import "time"

const (
	reactiveAdmissionBackoffBase = time.Second
	reactiveAdmissionBackoffMax  = 30 * time.Second
)

type admissionBackoffState struct {
	delay       time.Duration
	nextRetryAt time.Time
}

// admissionBackoffActive prevents a transient admission failure from causing
// a failed admission attempt on every scheduler tick. The state is per app so
// one capacity or vmmd problem cannot suppress unrelated applications.
func (t *Trigger) admissionBackoffActive(appID string, now time.Time) bool {
	if t == nil {
		return false
	}
	t.admissionMu.Lock()
	defer t.admissionMu.Unlock()
	state, ok := t.admissionBackoff[appID]
	return ok && now.Before(state.nextRetryAt)
}

func (t *Trigger) recordAdmissionFailure(appID string, now time.Time) {
	if t == nil {
		return
	}
	t.admissionMu.Lock()
	defer t.admissionMu.Unlock()
	if t.admissionBackoff == nil {
		t.admissionBackoff = make(map[string]admissionBackoffState)
	}
	state := t.admissionBackoff[appID]
	if state.delay <= 0 {
		state.delay = reactiveAdmissionBackoffBase
	} else if state.delay < reactiveAdmissionBackoffMax/2 {
		state.delay *= 2
	} else {
		state.delay = reactiveAdmissionBackoffMax
	}
	if state.delay > reactiveAdmissionBackoffMax {
		state.delay = reactiveAdmissionBackoffMax
	}
	state.nextRetryAt = now.Add(state.delay)
	t.admissionBackoff[appID] = state
}

func (t *Trigger) clearAdmissionBackoff(appID string) {
	if t == nil {
		return
	}
	t.admissionMu.Lock()
	delete(t.admissionBackoff, appID)
	t.admissionMu.Unlock()
}
