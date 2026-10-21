package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

func TestTCPListenerTLSDomainOwnership(t *testing.T) {
	valid := state.CustomDomain{Domain: "echo.example", AppID: "app", VerifiedAt: time.Now()}
	for _, tc := range []struct {
		name   string
		domain state.CustomDomain
		valid  bool
	}{
		{"verified-app-domain", valid, true},
		{"foreign-app", state.CustomDomain{Domain: "echo.example", AppID: "other", VerifiedAt: time.Now()}, false},
		{"unverified", state.CustomDomain{Domain: "echo.example", AppID: "app"}, false},
		{"environment-domain", state.CustomDomain{Domain: "echo.example", AppID: "app", EnvironmentID: "environment", VerifiedAt: time.Now()}, false},
		{"other-hostname", state.CustomDomain{Domain: "other.example", AppID: "app", VerifiedAt: time.Now()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := state.ValidateTCPListenerTLSDomain("app", "echo.example", tc.domain); (err == nil) != tc.valid {
				t.Fatalf("ownership validation = %v", err)
			}
		})
	}
	for _, ids := range [][2]string{{"", "echo.example"}, {"app", ""}} {
		if err := state.ValidateTCPListenerTLSDomain(ids[0], ids[1], valid); err == nil {
			t.Fatal("empty listener identity accepted")
		}
	}
}
