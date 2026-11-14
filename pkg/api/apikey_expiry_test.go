package api

import (
	"testing"
	"time"
)

func TestNewAPIKeyExpiry(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	year := now.Add(time.Duration(DefaultAPIKeyLifetimeDays) * 24 * time.Hour)
	for _, tc := range []struct {
		name   string
		scopes []string
		want   time.Time
	}{
		{"full access", []string{ScopeAdmin}, year},
		{"read only", []string{ScopeAppsRead}, year},
		{"agent", []string{ScopeAgent}, now.Add(AgentKeyLifetime)},
	} {
		got := NewAPIKeyExpiry(tc.scopes, now)
		if got == nil || !got.Equal(tc.want) {
			t.Errorf("%s: expiry = %v, want %v", tc.name, got, tc.want)
		}
	}
}
