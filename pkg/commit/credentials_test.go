package commit

import (
	"filippo.io/age"
	"github.com/google/uuid"
	"testing"
)

func TestConnectionCredentialSourceBindingAndRotation(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	source := uuid.NewString()
	raw := "postgres://relay:password@db.example.com/customer?sslmode=verify-full"
	blob, err := SealConnection(identity.Recipient(), source, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenConnection([]*age.X25519Identity{identity}, source, blob)
	if err != nil || got != raw {
		t.Fatal("credential round trip failed")
	}
	if _, err := OpenConnection([]*age.X25519Identity{identity}, uuid.NewString(), blob); err == nil {
		t.Fatal("credential usable for another source")
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenConnection([]*age.X25519Identity{other, identity}, source, blob); err != nil {
		t.Fatal("rotation overlap failed")
	}
	for _, bad := range []string{
		"postgres://relay:password@db.example.com/customer?sslmode=disable",
		"postgres://relay:password@db.example.com/customer?sslmode=require",
		raw + "&host=/tmp", raw + "&sslmode=disable", raw + "&options=arbitrary",
	} {
		if err := ValidateConnection(bad); err == nil {
			t.Fatal("accepted unsafe database configuration")
		}
	}
}
