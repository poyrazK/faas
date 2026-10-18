//go:build !no_pg

package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestPgStandardSnapshotWarmPublication(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotPublication(t, s, "warm")
}
func TestPgStandardSnapshotParkPublication(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotPublication(t, s, "park")
}
func TestPgStandardSnapshotNamespaceBeforeGrant(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	standardSnapshotNamespaceBeforeGrant(t, s)
}

type snapshotRawWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func insertStandardSnapshotRaw(ctx context.Context, q snapshotRawWriter, snap Snapshot) error {
	_, err := q.Exec(ctx, `INSERT INTO snapshots(deployment_id,fc_version,base_image_version,storage_key,mem_bytes,disk_bytes,stored_bytes,tier,application_standard_capture_token)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid)`, snap.DeploymentID, snap.FCVersion, snap.BaseImageVersion,
		snap.StorageKey, snap.MemBytes, snap.DiskBytes, snap.StoredBytes, snap.Tier, snap.ApplicationStandardCaptureToken)
	return mapErr(err)
}

func TestPgStandardSnapshotRawPublicationFence(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	a := standardSnapshotAck(g)
	snap := standardSnapshotCacheRow(ins.DeploymentID, g, a)
	if err := insertStandardSnapshotRaw(t.Context(), pool, snap); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal("unpublished raw capture accepted", err)
	}
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*Snapshot){
		func(s *Snapshot) { s.ApplicationStandardCaptureToken = "" },
		func(s *Snapshot) { s.ApplicationStandardCaptureToken = uuid.NewString() },
		func(s *Snapshot) { s.FCVersion += "-other" },
		func(s *Snapshot) { s.StorageKey += "-other" },
		func(s *Snapshot) { s.MemBytes++ },
		func(s *Snapshot) { s.DiskBytes++ },
		func(s *Snapshot) { s.Tier = SnapshotTierInit },
		func(s *Snapshot) { s.DeploymentID = uuid.NewString(); s.ApplicationStandardCaptureToken = "" },
		func(s *Snapshot) { s.DeploymentID = uuid.NewString() },
	} {
		bad := snap
		edit(&bad)
		if err := insertStandardSnapshotRaw(t.Context(), pool, bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatal("raw mismatch accepted", err)
		}
	}
	if err := insertStandardSnapshotRaw(t.Context(), pool, snap); err != nil {
		t.Fatal("raw matching capture refused", err)
	}
	stored, err := s.LatestSnapshot(t.Context(), ins.DeploymentID)
	if err != nil || stored.ApplicationStandardCaptureToken != g.Token {
		t.Fatal("raw insert lost association", err)
	}
	standardSnapshotRawImmutability(t, pool, stored)
}

func standardSnapshotRawImmutability(t *testing.T, q snapshotRawWriter, snap Snapshot) {
	t.Helper()
	for _, change := range []string{
		"application_standard_capture_token=NULL", "application_standard_capture_token=gen_random_uuid()",
		"storage_key=storage_key||'-other'", "fc_version=fc_version||'-other'", "base_image_version='other'",
		"mem_bytes=mem_bytes+1", "disk_bytes=disk_bytes+1", "tier='init'", "created_at=created_at+interval '1 second'",
		"deployment_id=gen_random_uuid()", "id=gen_random_uuid()",
	} {
		if _, err := q.Exec(t.Context(), "UPDATE snapshots SET "+change+" WHERE id=$1", snap.ID); !errors.Is(mapErr(err), ErrInvalidArgument) {
			t.Fatal("raw mutation rewrote immutable snapshot identity", change, err)
		}
	}
	if _, err := q.Exec(t.Context(), `UPDATE snapshots SET stale=true,delete_pending=true,stored_bytes=8192 WHERE id=$1`, snap.ID); err != nil {
		t.Fatal("cache lifecycle or physical allocation update refused", err)
	}
}

func insertStandardCaptureRaw(ctx context.Context, q snapshotRawWriter, state string, g runtimeadmission.SnapshotGrant) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	b := g.Parent.Binding
	_, err = q.Exec(ctx, `INSERT INTO application_standard_snapshot_captures(token,instance_id,app_id,deployment_id,account_id,node_id,parent_token,memory_key,expected_state,grant_data,input_snapshot)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,application_standard_lock_snapshot_capture($2,$9)->'input_snapshot')`,
		g.Token, b.InstanceID, b.AppID, b.DeploymentID, b.AccountID, b.NodeID, b.Token, g.MemoryKey, state, raw)
	return mapErr(err)
}

func alternateSnapshotNamespace(g runtimeadmission.SnapshotGrant) runtimeadmission.SnapshotGrant {
	g.Token = uuid.NewString()
	g.MemoryKey = SnapshotCaptureMemKey(g.Parent.Binding.DeploymentID, SnapshotTierWarm, g.Token)
	s := Snapshot{StorageKey: g.MemoryKey}
	g.VMStateKey, g.PrivateDriveKey = SnapshotVMStateKey(s), SnapshotDriveKey(s)
	return g
}

func TestPgStandardSnapshotNamespaceSerializes(t *testing.T) {
	for _, captureFirst := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmt.Sprintf("capture_first_%v/commit_%v", captureFirst, commit), func(t *testing.T) {
				standardSnapshotNamespaceRace(t, captureFirst, commit)
			})
		}
	}
}

func standardSnapshotNamespaceRace(t *testing.T, captureFirst, commit bool) {
	t.Helper()
	s, pool := runtimeCapturePGStore(t)
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	g = alternateSnapshotNamespace(g)
	snap := standardSnapshotCacheRow(ins.DeploymentID, g, standardSnapshotAck(g))
	snap.ApplicationStandardCaptureToken = "" // Competing legacy row has no proof.
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	first := func(q snapshotRawWriter) error { return insertStandardSnapshotRaw(t.Context(), q, snap) }
	second := func(q snapshotRawWriter) error { return insertStandardCaptureRaw(t.Context(), q, ins.State, g) }
	want := ErrConflict
	if captureFirst {
		first, second, want = second, first, ErrInvalidArgument
	}
	if err := first(tx); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- second(pool) }()
	finishStandardSnapshotNamespaceRace(t, pool, tx, result, second, g.MemoryKey, commit, want)
}

func waitStandardSnapshotNamespaceFence(t *testing.T, pool *pgxpool.Pool, result <-chan error, key string, allowBusy bool) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted
AND classid=((hashtextextended($1,43120261002)>>32)&4294967295)::oid
AND objid=(hashtextextended($1,43120261002)&4294967295)::oid AND objsubid=1)`, key).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return true
		}
		select {
		case err := <-result:
			if allowBusy && errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				return false
			}
			t.Fatal("competing namespace bypassed uncommitted owner", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("competing writer did not wait on namespace fence")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func finishStandardSnapshotNamespaceRace(t *testing.T, pool *pgxpool.Pool, tx pgx.Tx, result <-chan error, retry func(snapshotRawWriter) error, key string, commit bool, want error) {
	t.Helper()
	waiting := waitStandardSnapshotNamespaceFence(t, pool, result, key, errors.Is(want, ErrConflict))
	var err error
	if commit {
		err = tx.Commit(t.Context())
	} else {
		err = tx.Rollback(t.Context())
		want = nil
	}
	if err != nil {
		t.Fatal(err)
	}
	if !waiting {
		if err := retry(pool); !errors.Is(err, want) {
			t.Fatal("busy retry escaped namespace ownership", err, want)
		}
		return
	}
	select {
	case err := <-result:
		if !errors.Is(err, want) {
			t.Fatal("namespace ordering mismatch", err, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("namespace write did not resume")
	}
}

var _ snapshotRawWriter = (pgx.Tx)(nil)

// This narrowly exercises the new additive migration in an isolated clone.
// It does not stand in for the separately required frozen migration replay.
func TestPgStandardSnapshotPublicationLinkBackfill(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	ins, req := standardSnapshotFixture(t, s, "warm")
	g, err := s.IssueApplicationStandardSnapshotCapture(t.Context(), ins.State, req)
	if err != nil {
		t.Fatal(err)
	}
	a := standardSnapshotAck(g)
	if err := s.PublishApplicationStandardSnapshotCapture(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	valid := standardSnapshotCacheRow(ins.DeploymentID, g, a)
	other, err := s.CreateDeployment(t.Context(), Deployment{AppID: ins.AppID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	applySnapshotPublicationLinkFragment(t, pool, false)
	bad := valid
	bad.Tier, bad.MemBytes = SnapshotTierInit, bad.MemBytes+1
	legacy := valid
	legacy.DeploymentID, legacy.StorageKey = other.ID, SnapshotCaptureMemKey(other.ID, SnapshotTierWarm, uuid.NewString())
	for _, row := range []Snapshot{valid, bad, legacy} {
		if _, err := pool.Exec(t.Context(), `INSERT INTO snapshots(deployment_id,fc_version,storage_key,mem_bytes,disk_bytes,stored_bytes,tier)
VALUES($1,$2,$3,$4,$5,$6,$7)`, row.DeploymentID, row.FCVersion, row.StorageKey, row.MemBytes, row.DiskBytes, row.StoredBytes, row.Tier); err != nil {
			t.Fatal(err)
		}
	}
	applySnapshotPublicationLinkFragment(t, pool, true)
	for _, tc := range []struct {
		row   Snapshot
		token string
		stale bool
	}{{valid, g.Token, false}, {bad, "", true}, {legacy, "", false}} {
		var token string
		var stale bool
		err := pool.QueryRow(t.Context(), `SELECT coalesce(application_standard_capture_token::text,''),stale FROM snapshots WHERE deployment_id=$1 AND tier=$2`, tc.row.DeploymentID, tc.row.Tier).Scan(&token, &stale)
		if err != nil || token != tc.token || stale != tc.stale {
			t.Fatal("backfill upgraded mismatched history or changed legacy data", token, stale, err)
		}
	}
}

func applySnapshotPublicationLinkFragment(t *testing.T, pool *pgxpool.Pool, up bool) {
	t.Helper()
	raw, err := migrations.FS.ReadFile("20261002224615713_application_standard_snapshot_publication_link.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("additive migration lacks exactly one Down section")
	}
	section := parts[1]
	if up {
		section = parts[0]
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err := tx.Exec(t.Context(), section); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
}
