package main

import "testing"

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
		{name: "disabled true", explicit: map[string]bool{"disabled": true}, disabled: true, want: boolPtr(false)},
		{name: "enabled false keeps value semantics", explicit: map[string]bool{"enabled": true}, enabled: false, want: boolPtr(false)},
		{name: "enabled true", explicit: map[string]bool{"enabled": true}, enabled: true, want: boolPtr(true)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := triggerEnabledValue(tt.explicit, tt.enabled, tt.disabled)
			if (got == nil) != (tt.want == nil) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			if got != nil && *got != *tt.want {
				t.Fatalf("got %t, want %t", *got, *tt.want)
			}
		})
	}
}
