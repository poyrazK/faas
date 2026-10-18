package issueslack

import (
	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestSlackTokensRequireNamespaceAndKeyOverlap(t *testing.T) {
	old, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, namespace := range []string{state.IssueSlackTokenNamespace, "app-webhook-secret"} {
		blob, err := secretbox.SealBytes(old.Recipient(), namespace, []byte("xoxb-fake-token"), api.IssueSlackTokenMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		token, err := OpenToken([]*age.X25519Identity{current, old}, blob)
		if namespace == state.IssueSlackTokenNamespace {
			if err != nil || token != "xoxb-fake-token" {
				t.Fatal("key overlap", err)
			}
		} else if err == nil || token != "" {
			t.Fatal("wrong namespace accepted")
		}
		if _, err = OpenToken([]*age.X25519Identity{current}, blob); err == nil {
			t.Fatal("wrong key accepted")
		}
	}
}
