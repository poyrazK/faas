package secretbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
)

func TestLoadFleetAndHostKeys_TwelveNodesShareFleetAndKeepUniqueHosts(t *testing.T) {
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := SealOne(fleet.Recipient(), "APP_SECRET", "non-secret-probe", 1024)
	if err != nil {
		t.Fatal(err)
	}

	hostRecipients := make(map[string]struct{})
	for i := 0; i < 12; i++ {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "fleet.age"), []byte(fleet.String()), 0o400); err != nil {
			t.Fatal(err)
		}
		host, err := GenerateAndSaveHostKey(filepath.Join(dir, "host.age"))
		if err != nil {
			t.Fatal(err)
		}
		if host.Recipient().String() == fleet.Recipient().String() {
			t.Fatalf("node %d replaced its per-host identity with fleet identity", i)
		}
		if _, exists := hostRecipients[host.Recipient().String()]; exists {
			t.Fatalf("duplicate per-host identity at node %d", i)
		}
		hostRecipients[host.Recipient().String()] = struct{}{}

		identities, err := LoadFleetAndHostKeys(dir)
		if err != nil {
			t.Fatalf("node %d: %v", i, err)
		}
		if got := identities[0].Recipient().String(); got != fleet.Recipient().String() {
			t.Fatalf("node %d fleet recipient = %s, want %s", i, got, fleet.Recipient())
		}
		env, err := OpenMulti(identities, fixture)
		if err != nil || env["APP_SECRET"] != "non-secret-probe" {
			t.Fatalf("node %d cannot open shared customer-path fixture: env=%v err=%v", i, env, err)
		}
	}
	if len(hostRecipients) != 12 {
		t.Fatalf("unique host recipients=%d, want 12 (%s)", len(hostRecipients), fmt.Sprint(hostRecipients))
	}
}

func TestLoadFleetAndHostKeys_SystemdCredentialNames(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run", "credentials", "faas-apid.service")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fleet, _ := age.GenerateX25519Identity()
	host, _ := age.GenerateX25519Identity()
	for name, id := range map[string]*age.X25519Identity{
		"faas_fleet_age_identity": fleet,
		"faas_host_age_identity":  host,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(id.String()), 0o440); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := LoadFleetAndHostKeys(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0].Recipient().String() != fleet.Recipient().String() || ids[1].Recipient().String() != host.Recipient().String() {
		t.Fatalf("unexpected identity order: %+v", ids)
	}
}

func TestLoadFleetAndHostKeys_FailsClosedWithoutFleet(t *testing.T) {
	dir := t.TempDir()
	if _, err := GenerateAndSaveHostKey(filepath.Join(dir, "host.age")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFleetAndHostKeys(dir); err == nil {
		t.Fatal("expected missing fleet identity to fail closed")
	}
}
