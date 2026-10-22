package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// spec: false boolean action flags do not mutate trigger state.
func TestTriggerEnabledValue(t *testing.T) {
	tests := []struct {
		name     string
		explicit map[string]bool
		enabled  bool
		disabled bool
		want     *bool
	}{
		{name: "absent", explicit: map[string]bool{}, want: nil},
		{name: "disabled false is absent", explicit: map[string]bool{"disabled": true}, disabled: false, want: nil},
		{name: "disabled true", explicit: map[string]bool{"disabled": true}, disabled: true, want: main.BoolPtr(false)},
		{name: "enabled false keeps value semantics", explicit: map[string]bool{"enabled": true}, enabled: false, want: main.BoolPtr(false)},
		{name: "enabled true", explicit: map[string]bool{"enabled": true}, enabled: true, want: main.BoolPtr(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := main.TriggerEnabledValue(tt.explicit, tt.enabled, tt.disabled)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if got != nil && *got != *tt.want {
				t.Fatalf("got %t, want %t", *got, *tt.want)
			}
		})
	}
}

// A record delivered on its first try has no failed attempts; the line must
// not read as "never attempted" (H10-62).
func TestTriggerRecordLineNamesFailedAttempts(t *testing.T) {
	got := main.TriggerRecordLine(api.TriggerRecord{ID: "r1", State: "succeeded", Attempts: 0, ItemIdentifier: "item-1"})
	if !strings.Contains(got, "failed_attempts=0") || strings.Contains(got, " attempts=") {
		t.Fatalf("record line = %q, want failed_attempts=0", got)
	}
}
