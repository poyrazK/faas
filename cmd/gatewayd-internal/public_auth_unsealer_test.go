package main

import (
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestPublicAuthUnsealerUsesSharedProductionNamespace(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(identity.Recipient(), api.AppPublicAuthBasicSealNamespace,
		[]byte("audit-user\ntemporary-password"), api.AppPublicAuthBasicMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	unsealer := newPublicAuthUnsealer(func() []*age.X25519Identity { return []*age.X25519Identity{identity} })
	user, password, err := unsealer.UnsealBasicAuth(context.Background(), sealed)
	if err != nil {
		t.Fatal(err)
	}
	if user != "audit-user" || password != "temporary-password" {
		t.Fatal("shared namespace blob did not round-trip")
	}
}

func TestPublicAuthUnsealerRejectsWrongNamespace(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := secretbox.SealBytes(identity.Recipient(), "app_basic_auth",
		[]byte("user\npassword"), api.AppPublicAuthBasicMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	unsealer := newPublicAuthUnsealer(func() []*age.X25519Identity { return []*age.X25519Identity{identity} })
	if _, _, err := unsealer.UnsealBasicAuth(context.Background(), sealed); err == nil {
		t.Fatal("wrong namespace was accepted")
	}
}
