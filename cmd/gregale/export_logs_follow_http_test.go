package main

import "time"

// SetHTTPLogsFollowForTest makes `logs --follow --source http` poll without
// waiting and stop after maxPolls polls; the returned func restores it.
func SetHTTPLogsFollowForTest(interval time.Duration, maxPolls int) func() {
	oldInterval, oldMax := httpLogsFollowInterval, httpLogsFollowMaxPolls
	httpLogsFollowInterval, httpLogsFollowMaxPolls = interval, maxPolls
	return func() { httpLogsFollowInterval, httpLogsFollowMaxPolls = oldInterval, oldMax }
}

// SetRuntimeFollowIdleHintForTest shortens the runtime follow idle hint delay
// and re-arms the once-per-process guard; the returned func restores it.
func SetRuntimeFollowIdleHintForTest(after time.Duration) func() {
	old := runtimeFollowIdleHintAfter
	runtimeFollowIdleHintAfter = after
	runtimeFollowIdleHintDone.Store(false)
	return func() {
		runtimeFollowIdleHintAfter = old
		runtimeFollowIdleHintDone.Store(false)
	}
}

var StartRuntimeFollowIdleHint = startRuntimeFollowIdleHint
