// adr: 20261027090013 — a session from a seller image waits queued until the
// image-run job caching the image is ready, claims with the image's artifact
// as its drive, and fails with image_unavailable when the image cannot be had.
package agents_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/agents"
)

func TestSandboxFromAnImage(t *testing.T) {
	pool, s := agentdFixture(t)
	ctx := context.Background()
	job := func() string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO jobs (account_id) VALUES ($1) RETURNING id::text`, account).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pending, failing := job(), job()
	sb := admitSandbox(t, s, agents.SandboxAdmission{Preset: "image", ImageRef: "ghcr.io/acme/tools:1", ImageJobID: pending})
	doomed := admitSandbox(t, s, agents.SandboxAdmission{Preset: "image", ImageRef: "ghcr.io/acme/broken:1", ImageJobID: failing})
	if got, _ := s.Sandbox(ctx, account, sb.ID); got.ImageRef != "ghcr.io/acme/tools:1" {
		t.Fatalf("image ref %q", got.ImageRef)
	}
	if _, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5)); err != nil || found {
		t.Fatalf("claimed before the image was ready: %v %v", found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET image_materialization_status='failed', image_materialization_error='manifest unknown' WHERE id=$1`, failing); err != nil {
		t.Fatal(err)
	}
	if _, failed, _, err := s.ExpireSandboxLeases(ctx); err != nil || failed != 1 {
		t.Fatalf("expire = %d failed, %v", failed, err)
	}
	if got, _ := s.Sandbox(ctx, account, doomed.ID); got.State != agents.SandboxFailed || got.OutcomeCode != "image_unavailable" ||
		!strings.Contains(got.OutcomeMessage, "manifest unknown") {
		t.Fatalf("doomed session = %+v", got)
	}
	if got, _ := s.Sandbox(ctx, account, sb.ID); got.State != agents.SandboxQueued {
		t.Fatalf("pending-image session state %s", got.State)
	}
	if _, err := pool.Exec(ctx, `UPDATE jobs SET image_materialization_status='ready', image_storage_key='jobs/'||id||'.ext4' WHERE id=$1`, pending); err != nil {
		t.Fatal(err)
	}
	claim, found, err := s.ClaimSandbox(ctx, "agentd", "tok", ttl, maxLive(5))
	if err != nil || !found || claim.ID != sb.ID || claim.ImageKey != "jobs/"+pending+".ext4" || claim.ImageMissing {
		t.Fatalf("claim = %+v, %v, %v", claim, found, err)
	}
	// An image deleted between claim and boot shows as missing.
	if _, err := pool.Exec(ctx, `UPDATE jobs SET status='deleted' WHERE id=$1`, pending); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sandbox_sessions SET state='queued', lease_token=NULL, lease_owner=NULL, lease_expires_at=NULL WHERE id=$1`, sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := s.ClaimSandbox(ctx, "agentd", "tok2", ttl, maxLive(5)); found {
		t.Fatal("claimed a session whose image was deleted")
	}
}
