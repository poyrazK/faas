package referrals

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestShortCodeGenerationRetriesCollisions(t *testing.T) {
	pool := pgtest.Open(t)
	raw, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), string(raw)); err != nil {
		t.Fatal(err)
	}
	owner, other := uuid.NewString(), uuid.NewString()
	if _, err = pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan,status) VALUES ($1,'one@example.com','free','active'),($2,'two@example.com','free','active')", owner, other); err != nil {
		t.Fatal(err)
	}
	store := NewPostgresStore(pool)
	store.newCode = func() string { return "POJU43" }
	first, err := store.EnsureLink(t.Context(), owner, "sales")
	if err != nil || first.Code != "POJU43" {
		t.Fatal(first, err)
	}
	calls := 0
	store.newCode = func() string {
		calls++
		if calls == 1 {
			return "POJU43"
		}
		return "ABC123"
	}
	second, err := store.EnsureLink(t.Context(), other, "sales")
	if err != nil || second.Code != "ABC123" || calls != 2 {
		t.Fatal(second, err, calls)
	}
	again, err := store.EnsureLink(t.Context(), other, "sales")
	if err != nil || again.Code != second.Code {
		t.Fatal("retries changed an assigned code", again, err)
	}
	legacyOwner := uuid.NewString()
	legacyID := newLinkID()
	if _, err = pool.Exec(t.Context(), "INSERT INTO accounts(id,email,plan,status) VALUES ($1,'legacy@example.com','free','active')", legacyOwner); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(t.Context(), "INSERT INTO referral_links(code,owner_id,kind) VALUES ($1,$2,'customer')", legacyID, legacyOwner); err != nil {
		t.Fatal(err)
	}
	store.newCode = func() string { return "XYZ789" }
	upgraded, err := store.EnsureLink(t.Context(), legacyOwner, "customer")
	if err != nil || upgraded.Code != "XYZ789" {
		t.Fatal(upgraded, err)
	}
	var unchanged string
	if err = pool.QueryRow(t.Context(), "SELECT code FROM referral_links WHERE owner_id=$1", legacyOwner).Scan(&unchanged); err != nil || unchanged != legacyID {
		t.Fatal("historical identifier changed", unchanged, err)
	}
}

func TestShortCodeFormatAndNormalization(t *testing.T) {
	for range 100 {
		if code := NewCode(); !ValidCode(code) || len(code) != 6 {
			t.Fatal(code)
		}
	}
	if NormalizeCode(" poju43 ") != "POJU43" || !ValidCode("POJU43") {
		t.Fatal("human-entered code normalization failed")
	}
	for _, code := range []string{"POJU4", "POJU433", "PO-J43", "poju43", "<x>"} {
		if ValidCode(code) {
			t.Fatal("invalid code accepted", code)
		}
	}
}
