package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261108110000 against Postgres: a sealed header value is stored per
// app, read back only for its own app, listed for the edge re-seal, and
// re-sealed in place under the same id.
func TestPgStore_EdgeRuleHeaderSecrets(t *testing.T) {
	s, ctx := pgStore(t)
	acct, app := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "header-secrets")
	_, otherApp := pgEdgeRuleSeedAccount(t, s, ctx, api.PlanPro, "header-secrets-other")

	row, err := s.CreateEdgeRuleHeaderSecret(ctx, acct, app, []byte("sealed-v1"))
	if err != nil || row.ID == "" || row.AppID != app {
		t.Fatalf("create = %+v, %v", row, err)
	}
	got, err := s.EdgeRuleHeaderSecretsByID(ctx, app, []string{row.ID, "not-a-uuid", row.ID})
	if err != nil || len(got) != 1 || string(got[0].Ciphertext) != "sealed-v1" {
		t.Fatalf("by id = %+v, %v", got, err)
	}
	if got, err := s.EdgeRuleHeaderSecretsByID(ctx, otherApp, []string{row.ID}); err != nil || len(got) != 0 {
		t.Fatalf("another app read the value: %+v, %v", got, err)
	}

	var listed *state.EdgeSealedCredential
	afterKind, afterID := "", ""
	for listed == nil {
		page, err := s.ListEdgeSealedCredentials(ctx, afterKind, afterID, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for i := range page {
			if page[i].Kind == state.EdgeSealedCredentialHeaderSecret && page[i].ID == row.ID {
				listed = &page[i]
			}
		}
		if len(page) < 1000 {
			break
		}
		afterKind, afterID = page[len(page)-1].Kind, page[len(page)-1].ID
	}
	if listed == nil || string(listed.Sealed) != "sealed-v1" || listed.Managed {
		t.Fatalf("edge re-seal listing = %+v", listed)
	}
	replaced, err := s.ResealEdgeCredential(ctx, *listed, []byte("sealed-v2"))
	if err != nil || !replaced {
		t.Fatalf("reseal = %v, %v", replaced, err)
	}
	if replaced, err := s.ResealEdgeCredential(ctx, *listed, []byte("sealed-v3")); err != nil || replaced {
		t.Fatalf("stale reseal = %v, %v; want the newer ciphertext kept", replaced, err)
	}
	got, _ = s.EdgeRuleHeaderSecretsByID(ctx, app, []string{row.ID})
	if len(got) != 1 || string(got[0].Ciphertext) != "sealed-v2" {
		t.Fatalf("after reseal = %+v", got)
	}
}
