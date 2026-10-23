//go:build !no_pg

package state

import (
	"testing"
)

func TestPgStandardRebindAcrossRestart(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRebindAcrossRestart(t, s)
}

func TestPgStandardRebindPromotion(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRebindPromotion(t, s)
}

func TestPgStandardRebindPublication(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardRebindPublication(t, s)
}

// Re-binds are append-only: no UPDATE, and no DELETE while the instance
// lives; deleting the instance removes them with it.
func TestPgStandardRebindIsAppendOnly(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	standardRebindAcrossRestart(t, s)
	ctx := t.Context()
	var instance string
	if err := pool.QueryRow(ctx, `SELECT instance_id::text FROM instance_application_standard_rebinds LIMIT 1`).Scan(&instance); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE instance_application_standard_rebinds SET vmmd_epoch = vmmd_epoch + 1 WHERE instance_id = $1`, instance); err == nil {
		t.Fatal("re-bind updated")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM instance_application_standard_rebinds WHERE instance_id = $1`, instance); err == nil {
		t.Fatal("re-bind deleted while its instance lives")
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM instance_application_standard_rebinds WHERE instance_id = $1`, instance).Scan(&n); err != nil || n != 2 {
		t.Fatalf("re-binds after refused writes = %d, %v", n, err)
	}
	if _, err := pool.Exec(ctx, `SELECT application_standard_effective_incarnation($1::uuid, 'not-a-uuid')`, instance); err != nil {
		t.Fatalf("effective incarnation of a malformed issuer: %v", err)
	}
}

func TestPgStandardObservationRebindSnapshots(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardObservationRebindSnapshots(t, s)
}

// The guard stamps the producer once, at acknowledgment, and keeps it.
func TestPgStandardSnapshotProducerIsImmutable(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	var issued *string
	if err := pool.QueryRow(t.Context(), `SELECT producer_incarnation::text FROM application_standard_snapshot_captures WHERE token=$1`, g.Token).Scan(&issued); err != nil || issued != nil {
		t.Fatalf("producer before acknowledgment = %v, %v", issued, err)
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), standardSnapshotAck(g)); err != nil {
		t.Fatal(err)
	}
	if got := standardSnapshotGet(t, s, g).ProducerIncarnation; got != g.Parent.Binding.Incarnation {
		t.Fatalf("producer = %q, want %q", got, g.Parent.Binding.Incarnation)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE application_standard_snapshot_captures SET producer_incarnation=gen_random_uuid() WHERE token=$1`, g.Token); err == nil {
		t.Fatal("producer rewritten")
	}
}
