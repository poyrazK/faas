package leaser_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/leaser"
)

func TestPolicyValidate(t *testing.T) {
	cases := []struct {
		name   string
		policy leaser.Policy
		ok     bool
	}{
		{"ttl only", leaser.Policy{TTL: time.Second}, true},
		{"every bound", leaser.Policy{TTL: time.Second, MaxAttempts: 3, MaxDuration: time.Minute}, true},
		{"zero ttl", leaser.Policy{}, false},
		{"negative ttl", leaser.Policy{TTL: -time.Second}, false},
		{"negative attempts", leaser.Policy{TTL: time.Second, MaxAttempts: -1}, false},
		{"negative duration", leaser.Policy{TTL: time.Second, MaxDuration: -time.Second}, false},
		{"duration below ttl", leaser.Policy{TTL: time.Minute, MaxDuration: time.Second}, false},
	}
	for _, tc := range cases {
		err := tc.policy.Validate()
		if tc.ok && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, leaser.ErrInvalidPolicy) {
			t.Errorf("%s: got %v, want ErrInvalidPolicy", tc.name, err)
		}
	}
}
