package main

import (
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWebhookSecretFromRow(t *testing.T) {
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	sealed, err := sealdomain.SealGithubWebhookSecret([]*age.X25519Recipient{fleet.Recipient()}, secret)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		row        state.GithubWebhookSecret
		identities []*age.X25519Identity
		want       string
		wantErr    bool
	}{
		{"sealed copy wins over the plaintext column", state.GithubWebhookSecret{Value: []byte("stale-plaintext-value"), Sealed: sealed}, []*age.X25519Identity{fleet}, string(secret), false},
		{"row written before the sealed copy", state.GithubWebhookSecret{Value: secret}, []*age.X25519Identity{fleet}, string(secret), false},
		{"row written without plaintext", state.GithubWebhookSecret{Sealed: sealed}, []*age.X25519Identity{fleet}, string(secret), false},
		{"sealed copy that does not open fails closed", state.GithubWebhookSecret{Value: secret, Sealed: sealed}, []*age.X25519Identity{other}, "", true},
		{"sealed copy without an identity fails closed", state.GithubWebhookSecret{Value: secret, Sealed: sealed}, nil, "", true},
		{"empty row is not found", state.GithubWebhookSecret{}, []*age.X25519Identity{fleet}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := webhookSecretFromRow(tc.row, tc.identities)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("webhookSecretFromRow = %q, want an error", got)
				}
				return
			}
			if err != nil || string(got) != tc.want {
				t.Fatalf("webhookSecretFromRow = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
