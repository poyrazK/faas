package main

import (
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

func stagedSourceIdentity(t *testing.T) (string, *age.X25519Identity) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.age")
	if err := secretbox.WriteHostKeyAtPath(path, id); err != nil {
		t.Fatal(err)
	}
	return path, id
}

func TestLoadSourceSealDomain(t *testing.T) {
	path, domain := stagedSourceIdentity(t)
	fleetID, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fleet := fleetID.Recipient()
	for _, tc := range []struct {
		name           string
		env            map[string]string
		wantErr        bool
		wantStaged     bool
		wantRecipients []string
	}{
		{name: "not staged seals to the fleet", env: map[string]string{}, wantRecipients: []string{fleet.String()}},
		{name: "staged seals to both", env: map[string]string{envSealDomainSourceIdentity: path}, wantStaged: true, wantRecipients: []string{domain.Recipient().String(), fleet.String()}},
		{name: "fleet opts out", env: map[string]string{envSealDomainSourceIdentity: path, envSealDomainSourceWrites: "fleet"}, wantStaged: true, wantRecipients: []string{fleet.String()}},
		{name: "domain alone", env: map[string]string{envSealDomainSourceIdentity: path, envSealDomainSourceWrites: "domain"}, wantStaged: true, wantRecipients: []string{domain.Recipient().String()}},
		{name: "domain without the identity", env: map[string]string{envSealDomainSourceWrites: "domain"}, wantErr: true},
		{name: "dual without the identity", env: map[string]string{envSealDomainSourceWrites: "dual"}, wantErr: true},
		{name: "unknown mode", env: map[string]string{envSealDomainSourceWrites: "both"}, wantErr: true},
		{name: "configured identity missing", env: map[string]string{envSealDomainSourceIdentity: filepath.Join(t.TempDir(), "missing.age")}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := loadSourceSealDomain(func(k string) string { return tc.env[k] })
			if tc.wantErr {
				if err == nil {
					t.Fatal("loadSourceSealDomain succeeded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.staged() != tc.wantStaged {
				t.Fatalf("staged = %v, want %v", d.staged(), tc.wantStaged)
			}
			got, err := d.recipients(fleet)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.wantRecipients) {
				t.Fatalf("recipients = %v, want %v", got, tc.wantRecipients)
			}
			for i := range got {
				if got[i].String() != tc.wantRecipients[i] {
					t.Fatalf("recipients = %v, want %v", got, tc.wantRecipients)
				}
			}
		})
	}
}

func TestSourceSealDomainReaders(t *testing.T) {
	path, domain := stagedSourceIdentity(t)
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	d, err := loadSourceSealDomain(func(k string) string {
		if k == envSealDomainSourceIdentity {
			return path
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	got := d.readers(fleet, nil, fleet)
	if len(got) != 2 || got[0].Recipient().String() != domain.Recipient().String() || got[1].Recipient().String() != fleet.Recipient().String() {
		t.Fatalf("readers = %v; want the domain, then the fleet identity once", got)
	}
	// A token sealed to the domain alone opens with the readers.
	blob, err := secretbox.SealTo([]*age.X25519Recipient{domain.Recipient()}, secretbox.Envelope{installTokenSealKey: "ghs_test"})
	if err != nil {
		t.Fatal(err)
	}
	if env, err := secretbox.OpenMulti(got, blob); err != nil || env[installTokenSealKey] != "ghs_test" {
		t.Fatalf("OpenMulti = %v, %v", env, err)
	}
	if empty := (sourceSealDomain{}).readers(fleet); len(empty) != 1 {
		t.Fatalf("readers before staging = %v, want the fleet identity alone", empty)
	}
}
