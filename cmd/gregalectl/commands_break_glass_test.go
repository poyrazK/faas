package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestBreakGlassReasonAndKinds(t *testing.T) {
	for _, bad := range []string{"", "  ", "no", string(bytes.Repeat([]byte("x"), 401))} {
		if _, err := breakGlassReason(bad); err == nil {
			t.Errorf("breakGlassReason(%q) accepted", bad)
		}
	}
	if got, err := breakGlassReason("  leaked fleet key  "); err != nil || got != "leaked fleet key" {
		t.Fatalf("breakGlassReason = %q, %v", got, err)
	}
	if got := splitBreakGlassKinds(" sessions, ,api_keys "); len(got) != 2 || got[0] != "sessions" || got[1] != "api_keys" {
		t.Fatalf("splitBreakGlassKinds = %v", got)
	}
}

// TestBreakGlassPostgres drives the commands against PostgreSQL (ADR
// 20261022140000): pause writes the runtime configuration row daemons follow,
// a revoke without --yes only counts, and with --yes it ends every live
// session and API key in one audited transaction.
func TestBreakGlassPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// pgtest hands out a private clone; ConnString still names the base
	// database, so point the commands at the clone.
	u, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + pool.Config().ConnConfig.Database
	dsn := u.String()
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "break-glass-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateSession(ctx, uuid.NewString(), acct.ID, "192.0.2.1", "test"); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("gk_" + uuid.NewString()))
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash[:], "ci", []string{api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}

	if code := cmdBreakGlass([]string{"pause", "--pg-dsn", dsn, "--reason", "fleet key exposure"}); code != 0 {
		t.Fatalf("pause exit %d", code)
	}
	row, err := store.GetRuntimeConfig(ctx, runtimeconfig.KeyPlatformReleasePause, state.RuntimeConfigScopeGlobal, "")
	if err != nil {
		t.Fatal(err)
	}
	if paused, _ := runtimeconfig.Bool(row.DesiredValue); !paused || row.Reason != "break-glass pause: fleet key exposure" {
		t.Fatalf("pause row = %s %q", row.DesiredValue, row.Reason)
	}
	if code := cmdBreakGlass([]string{"pause", "--pg-dsn", dsn}); code == 0 {
		t.Fatal("pause without a reason succeeded")
	}

	before, err := store.RevokeAllCredentials(ctx, state.BreakGlassCredentialKinds, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if breakGlassCount(before, state.BreakGlassSessions) < 1 || breakGlassCount(before, state.BreakGlassAPIKeys) < 1 {
		t.Fatalf("live credentials before = %+v", before)
	}
	if code := cmdBreakGlass([]string{"revoke-credentials", "--pg-dsn", dsn}); code != 0 {
		t.Fatalf("dry run exit %d", code)
	}
	if again, _ := store.RevokeAllCredentials(ctx, state.BreakGlassCredentialKinds, "", "", true); breakGlassCount(again, state.BreakGlassSessions) != breakGlassCount(before, state.BreakGlassSessions) {
		t.Fatal("a dry run revoked sessions")
	}
	if code := cmdBreakGlass([]string{"revoke-credentials", "--pg-dsn", dsn, "--yes"}); code == 0 {
		t.Fatal("revoke without a reason succeeded")
	}
	if code := cmdBreakGlass([]string{"revoke-credentials", "--pg-dsn", dsn, "--yes", "--reason", "fleet key exposure"}); code != 0 {
		t.Fatalf("revoke exit %d", code)
	}
	after, err := store.RevokeAllCredentials(ctx, state.BreakGlassCredentialKinds, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range after {
		if c.Count != 0 {
			t.Fatalf("%d live %s after the revoke", c.Count, c.Kind)
		}
	}
	var data []byte
	if err := pool.QueryRow(ctx, `SELECT data FROM events WHERE kind = 'break_glass.credentials_revoked' ORDER BY id DESC LIMIT 1`).Scan(&data); err != nil {
		t.Fatalf("audit event: %v", err)
	}
	var event struct {
		Reason  string                       `json:"reason"`
		Revoked []state.BreakGlassRevocation `json:"revoked"`
	}
	if err := json.Unmarshal(data, &event); err != nil || event.Reason != "fleet key exposure" || breakGlassCount(event.Revoked, state.BreakGlassSessions) < 1 {
		t.Fatalf("audit event = %s, %v", data, err)
	}
	if _, err := store.RevokeAllCredentials(ctx, []string{"consumer_keys"}, breakGlassActor, "x", false); err == nil {
		t.Fatal("revoked an unknown credential kind")
	}
}

func breakGlassCount(rs []state.BreakGlassRevocation, kind string) int64 {
	for _, r := range rs {
		if r.Kind == kind {
			return r.Count
		}
	}
	return -1
}
