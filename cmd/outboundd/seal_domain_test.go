package main

import (
	"path/filepath"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestOutboundSealDomainLoaded(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(t.TempDir(), "outbound.age")
	if err := secretbox.WriteHostKeyAtPath(staged, id); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		path    string
		loaded  bool
		wantErr bool
	}{
		{name: "not staged", path: ""},
		{name: "staged", path: staged, loaded: true},
		{name: "configured but missing", path: filepath.Join(t.TempDir(), "missing.age"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loaded, err := outboundSealDomainLoaded(tc.path)
			if (err != nil) != tc.wantErr || loaded != tc.loaded {
				t.Fatalf("outboundSealDomainLoaded(%q) = %v, %v; want %v, error %v", tc.path, loaded, err, tc.loaded, tc.wantErr)
			}
		})
	}
}
