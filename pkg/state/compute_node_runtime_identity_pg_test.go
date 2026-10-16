//go:build !no_pg

package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestPgRuntimeIncarnationHistory(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	runtimeIncarnationLifecycle(t, s, func(n string) runtimeadmission.Identity {
		r := runtimeadmission.Identity{NodeID: n}
		err := pool.QueryRow(t.Context(), `SELECT vmmd_incarnation::text,vmmd_admission_protocol FROM compute_nodes WHERE id=$1`, n).Scan(&r.Incarnation, &r.ProtocolVersion)
		if err != nil {
			t.Fatal(err)
		}
		return r
	})
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM application_standard_native_incarnations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("node erasure retained startup history: %d %v", count, err)
	}
}

func TestPgRuntimeIncarnationRestartCannotReanimateGrant(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	runtimeIncarnationGrantReplay(t, s)
}

func TestPgRuntimeIncarnationRawHistoryProtection(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ctx := t.Context()
	node := runtimeIncarnationNode(t, s)
	a := runtimeadmission.Identity{NodeID: node.ID, Incarnation: uuid.NewString(), ProtocolVersion: runtimeadmission.ProtocolVersion}
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, a); err != nil {
		t.Fatal(err)
	}
	b := a
	b.Incarnation = uuid.NewString()
	if err := s.RegisterComputeNodeRuntimeIdentity(ctx, b); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, id string }{
		{`UPDATE compute_nodes SET vmmd_incarnation=$2 WHERE id=$1`, a.Incarnation},
		{`UPDATE compute_nodes SET vmmd_incarnation=NULL WHERE id=$1 AND $2::uuid IS NOT NULL`, a.Incarnation},
		{`INSERT INTO application_standard_native_incarnations(node_id,incarnation,protocol_version) VALUES($1,$2,1)`, uuid.NewString()},
	} {
		_, err := pool.Exec(ctx, tc.query, node.ID, tc.id)
		if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
			t.Fatalf("raw identity escape accepted: %v", err)
		}
	}

	for _, query := range []string{`DELETE FROM application_standard_native_incarnations WHERE node_id=$1`, `UPDATE application_standard_native_incarnations SET protocol_version=3 WHERE node_id=$1`} {
		if _, err := pool.Exec(ctx, query, node.ID); err == nil {
			t.Fatal("startup history mutable")
		}
	}
	var current string
	if err := pool.QueryRow(ctx, `SELECT vmmd_incarnation::text FROM compute_nodes WHERE id=$1`, node.ID).Scan(&current); err != nil || current != b.Incarnation {
		t.Fatalf("refusal changed current identity: %s %v", current, err)
	}
}

func TestPgRuntimeIncarnationPublishedReceiptRemainsFenced(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	runtimeIncarnationPublishedReceipt(t, s)
}
