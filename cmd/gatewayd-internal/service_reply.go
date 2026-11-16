package main

import "github.com/onebox-faas/faas/pkg/gateway/egresssink"

// serviceReplyRecorder feeds the service proxy's per-callee reply bytes into
// the egress stream meterd drains (billing review R8-2 slice 3). Nil without
// a sink.
func serviceReplyRecorder(sink *egresssink.EgressSink) func(instanceID string, n int64) {
	if sink == nil {
		return nil
	}
	return sink.RecordServiceReplyBytes
}
