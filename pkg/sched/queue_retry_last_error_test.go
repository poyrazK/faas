package sched_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched"
)

// A queue message whose handler threw was retried with last_error
// "broker_error", which reads as a platform fault (Hunt 12 H12-34).
func TestQueueRetryGroupsRecordWhatFailed(t *testing.T) {
	long := strings.Repeat("x", 600)
	tests := []struct {
		name   string
		items  []string
		status map[string]sched.TriggerDispatchResult
		want   map[string][]string
	}{
		{
			name:   "handler failure",
			items:  []string{"a"},
			status: map[string]sched.TriggerDispatchResult{"a": {Status: "retry", Code: "function_state_failed", Error: "function state=failed"}},
			want:   map[string][]string{"function_state_failed: function state=failed": {"a"}},
		},
		{
			name:  "grouped by reason in order",
			items: []string{"a", "b", "c"},
			status: map[string]sched.TriggerDispatchResult{
				"a": {Status: "retry", Code: "invoke_timeout"},
				"b": {Status: "broker_error", Error: "dial gatewayd: connection refused"},
				"c": {Status: "retry", Code: "invoke_timeout"},
			},
			want: map[string][]string{"invoke_timeout": {"a", "c"}, "dial gatewayd: connection refused": {"b"}},
		},
		{
			name:   "nothing reported",
			items:  []string{"a"},
			status: map[string]sched.TriggerDispatchResult{"a": {Status: "retry"}},
			want:   map[string][]string{"broker_error": {"a"}},
		},
		{
			name:  "missing result",
			items: []string{"a"},
			want:  map[string][]string{"missing dispatch result": {"a"}},
		},
		{
			name:   "never a terminal reason",
			items:  []string{"a"},
			status: map[string]sched.TriggerDispatchResult{"a": {Status: "retry", Error: "max_attempts"}},
			want:   map[string][]string{"broker_error": {"a"}},
		},
		{
			name:   "long error truncated",
			items:  []string{"a"},
			status: map[string]sched.TriggerDispatchResult{"a": {Status: "retry", Error: long}},
			want:   map[string][]string{long[:512]: {"a"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sched.QueueRetryGroups(tt.items, tt.status); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("groups = %v, want %v", got, tt.want)
			}
		})
	}
}
