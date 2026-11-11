//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// TestPgStore_ImageRunEvictionSkipsSessionImages pins that a runner job a live
// sandbox session boots from is never evicted, and is again once the session
// closes (ADR-20261027090013, seller-image sessions).
func TestPgStore_ImageRunEvictionSkipsSessionImages(t *testing.T) {
	s := newPgStore(t)
	ctx := context.Background()
	acct := uuid.NewString()
	if _, err := s.pool.Pool.Exec(ctx, `insert into accounts (id, email, plan, created_at) values ($1, $2, 'pro', now())`,
		acct, "img-sessions-"+acct[:8]+"@x.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	ensure := func(name string) (ImageRunEnsureResult, error) {
		return s.JobEnsureImageRun(ctx, ImageRunEnsure{AccountID: acct, Name: name, ImageRef: "ghcr.io/x/y:1",
			RAMMB: 512, TaskTimeoutSec: 60, MaxCached: 1})
	}
	a, err := ensure("ir-aaaaaaaa")
	if err != nil {
		t.Fatalf("ensure a: %v", err)
	}
	var session string
	if err := s.pool.Pool.QueryRow(ctx, `insert into sandbox_sessions (account_id, preset, ram_mb, idle_timeout_sec, expires_at,
		image_ref, image_job_id) values ($1, 'image', 1024, 60, now() + interval '1 hour', 'ghcr.io/x/y:1', $2) returning id::text`,
		acct, a.Job.ID).Scan(&session); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	var full *ImageRunCacheFullError
	if _, err := ensure("ir-bbbbbbbb"); !errors.As(err, &full) {
		t.Fatalf("ensure with a session on the only image err = %v, want cache full", err)
	}
	if _, err := s.pool.Pool.Exec(ctx, `update sandbox_sessions set state = 'closed', closed_at = now() where id = $1`, session); err != nil {
		t.Fatalf("close session: %v", err)
	}
	b, err := ensure("ir-bbbbbbbb")
	if err != nil || len(b.Evicted) != 1 || b.Evicted[0].ID != a.Job.ID {
		t.Fatalf("ensure after close = %+v, %v", b, err)
	}
}
