// adr: 20261028205920
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A used mcp-login key slides, but never past api.MCPLoginKeyMaxAge after it
// was created, and never shorter than it already is.
func TestPg_MCPLoginKeySlideStopsThirtyDaysAfterCreation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acctID := createAccount(t, s, ctx, pgTestEmail(t))
	soon := time.Now().Add(time.Hour)
	key, err := s.CreateAPIKeyWithExpiryAndProvenance(ctx, acctID, []byte{0xA6, 0xE7, 0x01}, api.MCPLoginKeyLabel, []string{api.ScopeAppsRead}, &soon, "192.0.2.1", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Created 29 days ago: a week from now would pass the 30-day bound.
	if _, err := pool.Exec(ctx, `update api_keys set created_at = now() - interval '29 days' where id = $1`, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchKeyLastUsed(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	var expires time.Time
	if err := pool.QueryRow(ctx, `select expires_at from api_keys where id = $1`, key.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if want := time.Now().Add(24 * time.Hour); expires.Before(want.Add(-time.Minute)) || expires.After(want.Add(time.Minute)) {
		t.Fatalf("expires %v, want about %v (30 days after creation)", expires, want)
	}

	// Past the bound already: the touch never shortens what is left.
	far := time.Now().Add(20 * 24 * time.Hour)
	if _, err := pool.Exec(ctx, `update api_keys set created_at = now() - interval '29 days', expires_at = $2 where id = $1`, key.ID, far); err != nil {
		t.Fatal(err)
	}
	if err := s.TouchKeyLastUsed(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select expires_at from api_keys where id = $1`, key.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires.Before(far.Add(-time.Second)) {
		t.Fatalf("touch shortened the key to %v", expires)
	}
}
