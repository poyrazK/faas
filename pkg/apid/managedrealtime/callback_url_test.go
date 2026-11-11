package managedrealtime

import "testing"

func TestValidateRealtimeCallbackURL(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"https://app.example.com", true},
		{"https://app.example.com/hooks/realtime?v=1", true},
		{"http://app.example.com", true},
		{"https://user:secret@app.example.com", false},
		{"https://token@app.example.com", false},
		{"https://app.example.com/#section", false},
		{"ftp://app.example.com", false},
		{"/relative", false},
		{"", false},
	} {
		if got := validateRealtimeCallbackURL(tc.raw) == nil; got != tc.ok {
			t.Errorf("validateRealtimeCallbackURL(%q) ok=%v, want %v", tc.raw, got, tc.ok)
		}
	}
}
