package githubd

import (
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

// TestSealInstallToken pins who opens an install token githubd seals: the
// Recipient alone by default, every one of Recipients once the source seal
// domain sets them (ADR 20261022111812).
func TestSealInstallToken(t *testing.T) {
	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	fleet, domain := newID(), newID()
	for _, tc := range []struct {
		name        string
		svc         *RealService
		fleetOpens  bool
		domainOpens bool
	}{
		{"recipient", &RealService{Recipient: fleet.Recipient()}, true, false},
		{"domain and fleet", &RealService{Recipient: fleet.Recipient(), Recipients: []*age.X25519Recipient{domain.Recipient(), fleet.Recipient()}}, true, true},
		{"domain alone", &RealService{Recipient: fleet.Recipient(), Recipients: []*age.X25519Recipient{domain.Recipient()}}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sealed, err := tc.svc.sealInstallToken("ghs_install")
			if err != nil {
				t.Fatal(err)
			}
			for who, want := range map[*age.X25519Identity]bool{fleet: tc.fleetOpens, domain: tc.domainOpens} {
				env, err := secretbox.Open(who, sealed)
				if opened := err == nil && env[installTokenSealKey] == "ghs_install"; opened != want {
					t.Errorf("%s opens = %v, want %v", who.Recipient(), opened, want)
				}
			}
		})
	}
	over := strings.Repeat("a", maxInstallTokenBytes+1)
	if _, err := (&RealService{Recipients: []*age.X25519Recipient{domain.Recipient()}}).sealInstallToken(over); err == nil {
		t.Fatal("sealed an install token over the cap")
	}
}
