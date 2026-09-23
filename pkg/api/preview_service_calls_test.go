package api

import "testing"

func TestPreviewServiceCallsPolicyEffective(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   PreviewServiceCallsPolicy
		want PreviewServiceCallsPolicy
	}{
		{"legacy", "", PreviewServiceCallsAllow},
		{"allow", PreviewServiceCallsAllow, PreviewServiceCallsAllow},
		{"deny", PreviewServiceCallsDeny, PreviewServiceCallsDeny},
		{"unknown", "future-policy", PreviewServiceCallsDeny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.Effective(); got != tc.want {
				t.Fatalf("effective policy = %q, want %q", got, tc.want)
			}
		})
	}
}
