package state

import (
	"testing"
	"time"
)

func TestTCPListenerTLSDomainOwnership(t *testing.T) {
	valid := CustomDomain{Domain: "echo.example", AppID: "app", VerifiedAt: time.Now()}
	for _, tc := range []struct {
		name   string
		domain CustomDomain
		valid  bool
	}{
		{"verified-app-domain", valid, true},
		{"foreign-app", CustomDomain{Domain: "echo.example", AppID: "other", VerifiedAt: time.Now()}, false},
		{"unverified", CustomDomain{Domain: "echo.example", AppID: "app"}, false},
		{"environment-domain", CustomDomain{Domain: "echo.example", AppID: "app", EnvironmentID: "environment", VerifiedAt: time.Now()}, false},
		{"other-hostname", CustomDomain{Domain: "other.example", AppID: "app", VerifiedAt: time.Now()}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateTCPListenerTLSDomain("app", "echo.example", tc.domain); (err == nil) != tc.valid {
				t.Fatalf("ownership validation = %v", err)
			}
		})
	}
	for _, ids := range [][2]string{{"", "echo.example"}, {"app", ""}} {
		if err := ValidateTCPListenerTLSDomain(ids[0], ids[1], valid); err == nil {
			t.Fatal("empty listener identity accepted")
		}
	}
}
