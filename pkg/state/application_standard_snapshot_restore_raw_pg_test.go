//go:build !no_pg

package state

// adr: 593. Raw SQL must preserve catalog authority independently of Go stores.
// Native receipts in these fixtures are simulations of verified cold fallback.

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"google.golang.org/protobuf/proto"
)

func TestPgStandardRestoreEvidenceWireMatchesProtobuf(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f := rawStandardRestoreFixture(t, s)
	for _, tc := range []struct {
		name string
		edit func(*runtimeadmission.SnapshotRestoreEvidence)
	}{
		{"catalog", func(_ *runtimeadmission.SnapshotRestoreEvidence) {}},
		{"UTF8 and long length", func(e *runtimeadmission.SnapshotRestoreEvidence) {
			e.Capture.Parent.Netns = strings.Repeat("日志\\\"", 12000)
		}},
		{"all scalar fields and nested restore binding", func(e *runtimeadmission.SnapshotRestoreEvidence) {
			e.Version = 1<<32 - 1
			e.Capture.Parent.LeaseUID = 1<<31 - 1
			e.Capture.Parent.Method, e.Capture.Parent.Paused = vmmdpb.WakeMethod_WAKE_RESTORE, true
			e.Capture.Parent.ArtifactConsumption.ProcessPID = 1<<32 - 1
			e.Capture.Parent.Binding.DesiredRevision = 1<<63 - 1
			e.Capture.Parent.Binding.SnapshotCaptureToken = f.Binding.SnapshotCaptureToken
			e.Capture.Parent.Binding.SnapshotEvidenceHash = f.Binding.SnapshotEvidenceHash
		}},
		{"drive order and maximum count", func(e *runtimeadmission.SnapshotRestoreEvidence) {
			drives := e.Capture.Parent.ArtifactConsumption.Drives
			for len(drives) < api.SidecarCapMax+2 {
				drives = append(drives, drives[0])
			}
			for i, j := 0, len(drives)-1; i < j; i, j = i+1, j-1 {
				drives[i], drives[j] = drives[j], drives[i]
			}
			e.Capture.Parent.ArtifactConsumption.Drives = drives
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := f.Evidence.Clone()
			tc.edit(&e)
			want, err := (proto.MarshalOptions{Deterministic: true}).Marshal(e.ToProto())
			if err != nil {
				t.Fatal(err)
			}
			var got []byte
			if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_wire_message('evidence',$1::jsonb)`, restoreRawJSON(t, e)).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("SQL wire differs from generated protobuf: got %d bytes want %d", len(got), len(want))
			}
		})
	}
	var hash string
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_evidence_hash($1::uuid,$2,$3::jsonb)`, f.Grant.Token, f.Grant.FCVersion, restoreRawJSON(t, f.Evidence.Capture)).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	want, err := f.Evidence.Hash()
	if err != nil || hash != want || hash != f.Binding.SnapshotEvidenceHash {
		t.Fatal("SQL catalog hash differs from immutable Go evidence", hash, want, err)
	}
}

func rawStandardRestoreFixture(t *testing.T, s *PgStore) standardRestoreFixture {
	t.Helper()
	f := standardRestoreTestFixture(t, s)
	// Use storage's actual clock and producer deadline without calling the Go
	// issuer. Raw writes still independently pass every grant/publication guard.
	var raw []byte
	if err := s.pool.QueryRow(t.Context(), `SELECT application_standard_lock_native_boot($1::uuid,$2)`, f.Target.ID, f.Target.State).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var clock struct {
		Now      int64 `json:"clock_unix_nano"`
		Deadline int64 `json:"artifact_expires_at_unix_nano"`
	}
	if err := json.Unmarshal(raw, &clock); err != nil {
		t.Fatal(err)
	}
	f.Binding.IssuedAtUnixNano = clock.Now
	f.Binding.ExpiresAtUnixNano = clock.Now + int64(api.ApplicationStandardRuntimeAdmissionTTL)
	if clock.Deadline > 0 && clock.Deadline < f.Binding.ExpiresAtUnixNano {
		f.Binding.ExpiresAtUnixNano = clock.Deadline
	}
	return f
}

func restoreRawJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func insertRawRestoreGrant(t *testing.T, pool *pgxpool.Pool, f standardRestoreFixture) error {
	t.Helper()
	_, err := pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, f.Binding.Token, f.Target.ID, f.Target.State, restoreRawJSON(t, f.Binding))
	return err
}

func recordRawRestoreReceipt(t *testing.T, pool *pgxpool.Pool, r runtimeadmission.Receipt) error {
	t.Helper()
	_, err := pool.Exec(t.Context(), `UPDATE instance_application_standard_boots SET receipt=$2::jsonb,received_at=clock_timestamp() WHERE token=$1`, r.Binding.Token, restoreRawJSON(t, r))
	return err
}

func publishRawRestoreReceipt(t *testing.T, pool *pgxpool.Pool, r runtimeadmission.Receipt) error {
	t.Helper()
	return publishRawRestoreTuple(t, pool, r, string(StateRunning))
}

func publishRawRestoreTuple(t *testing.T, pool *pgxpool.Pool, r runtimeadmission.Receipt, next string) error {
	t.Helper()
	if next == string(StateWaking) {
		// This writer publishes only the network tuple. Reassigning WAKING
		// after acknowledgment correctly trips the separate receipt-reuse fence.
		_, err := pool.Exec(t.Context(), `UPDATE instances SET application_standard_boot_token=$2,netns=$3,host_ip=$4::inet,guest_uid=$5 WHERE id=$1`, r.Binding.InstanceID, r.Binding.Token, r.Netns, r.HostIP, r.LeaseUID)
		return err
	}
	_, err := pool.Exec(t.Context(), `UPDATE instances SET application_standard_boot_token=$2,netns=$3,host_ip=$4::inet,guest_uid=$5,state=$6,started_at=clock_timestamp() WHERE id=$1`, r.Binding.InstanceID, r.Binding.Token, r.Netns, r.HostIP, r.LeaseUID, next)
	return err
}

func assertRawRestoreRefused(t *testing.T, err error, busy bool) {
	t.Helper()
	code, constraint := "23514", "application_standard_runtime_stale"
	if busy {
		code, constraint = "55P03", "application_standard_runtime_busy"
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code || pgErr.ConstraintName != constraint {
		t.Fatalf("raw restore authority bypassed expected %s/%s: %v", code, constraint, err)
	}
}

func TestPgStandardSnapshotRestoreRawGrantRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, *PgStore, *standardRestoreFixture)
	}{
		{"unknown catalog", func(_ *testing.T, _ *PgStore, f *standardRestoreFixture) {
			f.Binding.SnapshotCaptureToken = uuid.NewString()
		}},
		{"changed evidence", func(_ *testing.T, _ *PgStore, f *standardRestoreFixture) {
			f.Binding.SnapshotEvidenceHash = strings.Repeat("0", 64)
		}},
		{"partial token", func(_ *testing.T, _ *PgStore, f *standardRestoreFixture) { f.Binding.SnapshotEvidenceHash = "" }},
		{"partial hash", func(_ *testing.T, _ *PgStore, f *standardRestoreFixture) { f.Binding.SnapshotCaptureToken = "" }},
		{"different account", func(_ *testing.T, _ *PgStore, f *standardRestoreFixture) { f.Binding.AccountID = uuid.NewString() }},
		{"stale cache", func(t *testing.T, s *PgStore, f *standardRestoreFixture) {
			if err := s.MarkSnapshotStale(t.Context(), f.Snapshot.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"deleted cache", func(t *testing.T, s *PgStore, f *standardRestoreFixture) {
			if _, err := s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID}); err != nil {
				t.Fatal(err)
			}
		}},
		{"GC cache", func(t *testing.T, s *PgStore, f *standardRestoreFixture) {
			if _, err := s.MarkOldSnapshotsStale(t.Context(), []string{f.Snapshot.ID}); err != nil {
				t.Fatal(err)
			}
		}},
		{"current RAM", func(t *testing.T, s *PgStore, f *standardRestoreFixture) {
			ram := f.Target.RAMMB * 2
			if _, err := s.UpdateApp(t.Context(), f.Target.AppID, UpdateAppParams{RAMMB: &ram}); err != nil {
				t.Fatal(err)
			}
		}},
		{"caller rewrote memory", func(t *testing.T, _ *PgStore, f *standardRestoreFixture) {
			f.Evidence.Capture.Memory.Digest = "sha256:" + strings.Repeat("0", 64)
			var err error
			f.Binding.SnapshotEvidenceHash, err = f.Evidence.Hash()
			if err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			f := rawStandardRestoreFixture(t, s)
			tc.edit(t, s, &f)
			assertRawRestoreRefused(t, insertRawRestoreGrant(t, pool, f), false)
			assertStandardRestoreNoGrant(t, s, f.Binding.Token)
			assertNativeBootUnpublished(t, s, f.Target)
		})
	}
}

func TestPgStandardSnapshotRestoreRawReceiptAndPublicationFence(t *testing.T) {
	for _, phase := range []string{"receipt", "waking network tuple", "publication"} {
		for _, invalidation := range []string{"stale", "delete", "GC"} {
			t.Run(phase+"/"+invalidation, func(t *testing.T) {
				s, pool := runtimeCapturePGStore(t)
				f := rawStandardRestoreFixture(t, s)
				if err := insertRawRestoreGrant(t, pool, f); err != nil {
					t.Fatal("current raw grant refused", err)
				}
				r := consumedNativeReceipt(f.Binding, f.Capture)
				if phase != "receipt" {
					if err := recordRawRestoreReceipt(t, pool, r); err != nil {
						t.Fatal("current raw receipt refused", err)
					}
				}
				var err error
				switch invalidation {
				case "stale":
					err = s.MarkSnapshotStale(t.Context(), f.Snapshot.ID)
				case "delete":
					_, err = s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID})
				case "GC":
					_, err = s.MarkOldSnapshotsStale(t.Context(), []string{f.Snapshot.ID})
				}
				if err != nil {
					t.Fatal(err)
				}
				if phase == "receipt" {
					err = recordRawRestoreReceipt(t, pool, r)
				} else if phase == "waking network tuple" {
					err = publishRawRestoreTuple(t, pool, r, f.Target.State)
				} else {
					err = publishRawRestoreReceipt(t, pool, r)
				}
				assertRawRestoreRefused(t, err, false)
				assertNativeBootUnpublished(t, s, f.Target)
				var received bool
				if err := pool.QueryRow(t.Context(), `SELECT receipt IS NOT NULL FROM instance_application_standard_boots WHERE token=$1`, f.Binding.Token).Scan(&received); err != nil || received != (phase != "receipt") {
					t.Fatal("refusal changed immutable acknowledgment", received, err)
				}
			})
		}
	}
}

func TestPgStandardSnapshotRestoreRawAuthorityAndResidentHistory(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f := rawStandardRestoreFixture(t, s)
	if err := insertRawRestoreGrant(t, pool, f); err != nil {
		t.Fatal(err)
	}
	r := consumedNativeReceipt(f.Binding, f.Capture)
	if err := recordRawRestoreReceipt(t, pool, r); err != nil {
		t.Fatal(err)
	}
	if err := publishRawRestoreTuple(t, pool, r, f.Target.State); err != nil {
		t.Fatal("current waking network tuple refused", err)
	}
	if err := publishRawRestoreReceipt(t, pool, r); err != nil {
		t.Fatal("current raw publication refused", err)
	}
	if err := s.MarkSnapshotStale(t.Context(), f.Snapshot.ID); err != nil {
		t.Fatal(err)
	}
	// A consumed cache may be collected without retroactively revoking a
	// resident cold-fallback receipt or making ordinary bookkeeping impossible.
	if _, err := pool.Exec(t.Context(), `UPDATE instances SET last_request_at=clock_timestamp() WHERE id=$1`, f.Target.ID); err != nil {
		t.Fatal("resident bookkeeping required its old cache", err)
	}
	if _, err := s.DeleteSnapshotsByID(t.Context(), []string{f.Snapshot.ID}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), f.Target.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal("cleanup depended on a cache", err)
	}
	if err := s.DeleteInstance(t.Context(), f.Target.ID); err != nil {
		t.Fatal(err)
	}
	if saved := standardSnapshotGet(t, s, f.Grant); !saved.Grant.Equal(f.Grant) {
		t.Fatal("cleanup rewrote historical catalog")
	}
}

func TestPgStandardSnapshotRestoreRawWakingCleanup(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f := rawStandardRestoreFixture(t, s)
	if err := insertRawRestoreGrant(t, pool, f); err != nil {
		t.Fatal(err)
	}
	r := consumedNativeReceipt(f.Binding, f.Capture)
	if err := recordRawRestoreReceipt(t, pool, r); err != nil {
		t.Fatal(err)
	}
	if err := publishRawRestoreTuple(t, pool, r, f.Target.State); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSnapshotStale(t.Context(), f.Snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateInstanceStateToTerminal(t.Context(), f.Target.ID, string(StateStopped), time.Now()); err != nil {
		t.Fatal("waking cleanup required a current cache", err)
	}
	if err := s.DeleteInstance(t.Context(), f.Target.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPgStandardRestoreCatalogRAMLimit(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	f := rawStandardRestoreFixture(t, s)
	ack := *standardSnapshotGet(t, s, f.Grant).Acknowledgment
	var input map[string]any
	if err := json.Unmarshal(f.Capture.inputs, &input); err != nil {
		t.Fatal(err)
	}
	for _, ram := range []int64{api.ApplicationStandardSnapshotMaxArtifactBytes >> 20, (api.ApplicationStandardSnapshotMaxArtifactBytes >> 20) + 1} {
		// This tests the catalog projection's RAM boundary. The raw admission
		// path independently requires immutable current native inputs.
		input["instance_ram_mb"] = ram
		ack.Capture.Memory.Bytes = ram << 20
		var hash string
		if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_evidence_hash($1::uuid,$2,$3::jsonb)`, f.Grant.Token, f.Grant.FCVersion, restoreRawJSON(t, ack.Capture)).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		b := f.Binding
		b.SnapshotEvidenceHash = hash
		snapshot := map[string]any{"storage_key": f.Snapshot.StorageKey, "fc_version": f.Snapshot.FCVersion, "mem_bytes": ack.Capture.Memory.Bytes, "disk_bytes": f.Snapshot.DiskBytes}
		var valid bool
		if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_catalog_matches($1::jsonb,$2::jsonb,$3::jsonb,$4::jsonb,$2::jsonb,$5::jsonb)`, restoreRawJSON(t, b), restoreRawJSON(t, input), restoreRawJSON(t, f.Grant), restoreRawJSON(t, ack), restoreRawJSON(t, snapshot)).Scan(&valid); err != nil {
			t.Fatal(err)
		}
		if valid != (ram<<20 <= api.ApplicationStandardSnapshotMaxArtifactBytes) {
			t.Fatal("SQL restore RAM differs from central artifact cap", ram, valid)
		}
	}
}

func TestPgStandardSnapshotRestoreRawCatalogContention(t *testing.T) {
	for _, table := range []string{"catalog", "cache"} {
		t.Run(table, func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			f := rawStandardRestoreFixture(t, s)
			for _, phase := range []string{"grant", "receipt", "publication"} {
				tx, err := pool.Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(t.Context())
				if table == "catalog" {
					_, err = tx.Exec(t.Context(), `SELECT 1 FROM application_standard_snapshot_captures WHERE token=$1 FOR UPDATE`, f.Grant.Token)
				} else {
					_, err = tx.Exec(t.Context(), `SELECT 1 FROM snapshots WHERE id=$1 FOR UPDATE`, f.Snapshot.ID)
				}
				if err != nil {
					t.Fatal(err)
				}
				r := consumedNativeReceipt(f.Binding, f.Capture)
				run := func() error {
					switch phase {
					case "grant":
						return insertRawRestoreGrant(t, pool, f)
					case "receipt":
						return recordRawRestoreReceipt(t, pool, r)
					default:
						return publishRawRestoreReceipt(t, pool, r)
					}
				}
				started := time.Now()
				assertRawRestoreRefused(t, run(), true)
				if time.Since(started) > time.Second {
					t.Fatal("raw writer waited while holding parent locks")
				}
				if err := tx.Rollback(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := run(); err != nil {
					t.Fatal("catalog contention poisoned retry", phase, err)
				}
			}
		})
	}
}
