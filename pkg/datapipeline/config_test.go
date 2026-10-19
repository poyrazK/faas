package datapipeline

import (
	"os"
	"path/filepath"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
)

func TestWorkerKeysRequireMatchingRecipientAndKeepRotationOverlap(t *testing.T) {
	current, _ := age.GenerateX25519Identity()
	previous, _ := age.GenerateX25519Identity()
	dir := t.TempDir()
	identity := filepath.Join(dir, "pipeline.age")
	recipient := identity + ".pub"
	for name, value := range map[string]string{identity: current.String(), identity + ".previous": previous.String(), recipient: current.Recipient().String()} {
		if err := os.WriteFile(name, []byte(value), 0400); err != nil {
			t.Fatal(err)
		}
	}
	c := Config{Enabled: true, Accounts: []string{uuid.NewString()}, Hosts: []string{"db.example"}, CIDRs: []string{"203.0.113.4/32"}, IdentityPath: identity, RecipientPath: recipient}
	if _, err := c.Policy(); err != nil {
		t.Fatal(err)
	}
	keys, err := c.LoadIdentities()
	if err != nil || len(keys) != 2 || keys[0].String() != current.String() || keys[1].String() != previous.String() {
		t.Fatal("rotation overlap unavailable", err)
	}
	if err = os.Chmod(recipient, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(recipient, []byte(previous.Recipient().String()+"\n"), 0400); err != nil {
		t.Fatal(err)
	}
	if _, err = c.LoadIdentities(); err == nil {
		t.Fatal("worker accepted a sealing key it cannot currently decrypt")
	}
	c.Hosts = []string{"*.example"}
	if _, err = c.Policy(); err == nil {
		t.Fatal("wildcard destination policy accepted")
	}
	c.Enabled = false
	if _, err = c.LoadIdentities(); err == nil {
		t.Fatal("disabled worker started")
	}
}
