package sched

import "time"

// triggerHealthWriteInterval bounds how often a consumer whose poll outcome
// has not changed rewrites its trigger_consumer_health row. Readers call a
// consumer stale after api.QueueConsumerMaxPollAge (30 s), so 10 s keeps
// last_poll_at fresh with margin. Every poll used to write the row: on
// production-us about 11 durable writes a second for six triggers, each
// waiting on a WAL flush, while nothing changed.
const triggerHealthWriteInterval = 10 * time.Second

// triggerHealthWrite is the last health row a Loop wrote for one trigger.
type triggerHealthWrite struct {
	at      time.Time
	errText string // "" for a successful poll
}

// triggerHealthDue reports whether a poll at `at` with outcome errText must be
// written: the first poll, a changed outcome (success, failure, or a
// different error), or the interval elapsed since the last write.
func (l *Loop) triggerHealthDue(triggerID string, at time.Time, errText string) bool {
	l.triggerHealthMu.Lock()
	defer l.triggerHealthMu.Unlock()
	last, ok := l.triggerHealthLast[triggerID]
	return !ok || last.errText != errText || at.Sub(last.at) >= triggerHealthWriteInterval || at.Before(last.at)
}

// triggerHealthWritten records a successful health write.
func (l *Loop) triggerHealthWritten(triggerID string, at time.Time, errText string) {
	l.triggerHealthMu.Lock()
	defer l.triggerHealthMu.Unlock()
	if l.triggerHealthLast == nil {
		l.triggerHealthLast = map[string]triggerHealthWrite{}
	}
	l.triggerHealthLast[triggerID] = triggerHealthWrite{at: at, errText: errText}
}
