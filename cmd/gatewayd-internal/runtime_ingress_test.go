package main

// adr: 612

import (
	"testing"

	"github.com/google/uuid"
)

func TestPrivateRuntimeIngressIdentityDefaultsOffAndRequiresDrain(t *testing.T) {
	for _, tc := range []struct {
		enabled, routing, drain, token string
		valid                          bool
	}{
		{"", "", "", "", true},
		{"1", "", "", "", false},
		{"1", "1", "", "", false},
		{"1", "1", "1", "bad", false},
		{"1", "1", "1", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", true},
	} {
		getenv := func(key string) string {
			switch key {
			case "FAAS_RUNTIME_UPGRADE_INGRESS_CONFIRMATION":
				return tc.enabled
			case "FAAS_RUNTIME_UPGRADE_ROUTING_CONFIRMATION":
				return tc.routing
			case "FAAS_RUNTIME_UPGRADE_DRAIN_CONFIRMATION":
				return tc.drain
			default:
				return tc.token
			}
		}
		h, err := privateRuntimeIngressIdentity(getenv, uuid.NewString(), uuid.NewString())
		if (err == nil) != tc.valid || (tc.enabled == "" && h != nil) || (tc.enabled == "1" && tc.valid && h == nil) {
			t.Fatal(tc, err)
		}
	}
}
