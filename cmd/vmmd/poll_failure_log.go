package main

// pollFailureLog limits a per-instance poll failure to one log line per
// instance and error text. The egress pollers run every few hundred
// milliseconds, and a VM whose veth or netns had vanished logged the same
// failure on every tick: on production-us, 32 such instances wrote 646,331
// "egress rx_bytes read failed" lines in three hours and pushed the vmmd
// journal over its alert limit (H10-60). Callers still count every failure in
// their metrics.
type pollFailureLog struct {
	last map[string]string
}

// failed records a failure and reports whether to log it: the first failure
// for key, or one whose error text changed.
func (l *pollFailureLog) failed(key, errText string) bool {
	if l.last == nil {
		l.last = make(map[string]string)
	}
	if prev, ok := l.last[key]; ok && prev == errText {
		return false
	}
	l.last[key] = errText
	return true
}

// succeeded clears key and reports whether it had been failing.
func (l *pollFailureLog) succeeded(key string) bool {
	if _, ok := l.last[key]; !ok {
		return false
	}
	delete(l.last, key)
	return true
}

// retain drops keys that keep rejects, so an instance that left the live set
// does not hold memory and a recycled id logs afresh.
func (l *pollFailureLog) retain(keep func(key string) bool) {
	for key := range l.last {
		if !keep(key) {
			delete(l.last, key)
		}
	}
}
