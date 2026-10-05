//go:build !no_pg

// adr: 595. Immutable restore hashes use the existing generated wire contract.
package migrations_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func TestMigrations_StandardRestoreWireContract(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	// Derive field numbers and kinds from generated descriptors, rather than
	// repeating the SQL field list in the test. Protocol drift fails loudly.
	for kind, message := range map[string]proto.Message{
		"evidence":             &vmmdpb.RuntimeSnapshotRestoreEvidence{},
		"capture":              &vmmdpb.RuntimeSnapshotCapture{},
		"artifact":             &vmmdpb.RuntimeCapturedArtifact{},
		"source":               &vmmdpb.RuntimeArtifactSource{},
		"drive":                &vmmdpb.RuntimeConsumedDrive{},
		"consumption":          &vmmdpb.RuntimeArtifactConsumption{},
		"snapshot-consumption": &vmmdpb.RuntimeSnapshotConsumption{},
		"receipt":              &vmmdpb.RuntimeBootReceipt{},
		"binding":              &vmmdpb.RuntimeBootBinding{},
	} {
		t.Run(kind, func(t *testing.T) {
			var raw []byte
			if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_wire_fields($1)`, kind).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var fields [][]json.RawMessage
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			descriptor := message.ProtoReflect().Descriptor().Fields()
			if len(fields) != descriptor.Len() {
				t.Fatal("SQL omitted generated message fields", kind)
			}
			for _, field := range fields {
				if len(field) != 3 {
					t.Fatal("invalid SQL field descriptor")
				}
				var number protoreflect.FieldNumber
				var name, fieldKind string
				if err := json.Unmarshal(field[0], &number); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(field[1], &name); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(field[2], &fieldKind); err != nil {
					t.Fatal(err)
				}
				d := descriptor.ByNumber(number)
				if d == nil || string(d.Name()) != name {
					t.Fatal("SQL field differs from generated protobuf", number, name)
				}
				want := map[protoreflect.Kind]string{protoreflect.StringKind: "string", protoreflect.Uint32Kind: "u32", protoreflect.Int32Kind: "i32", protoreflect.Int64Kind: "i64", protoreflect.BoolKind: "bool", protoreflect.EnumKind: "i32"}[d.Kind()]
				if d.Kind() == protoreflect.MessageKind {
					want = map[protoreflect.Name]string{"RuntimeBootBinding": "binding", "RuntimeBootReceipt": "receipt", "RuntimeSnapshotCapture": "capture", "RuntimeCapturedArtifact": "artifact", "RuntimeArtifactSource": "source", "RuntimeArtifactConsumption": "consumption", "RuntimeSnapshotConsumption": "snapshot-consumption"}[d.Message().Name()]
					if d.IsList() && d.Message().Name() == "RuntimeConsumedDrive" {
						want = "drives"
					}
				}
				if want == "" || want != fieldKind {
					t.Fatal("SQL wire type differs from generated protobuf", name, fieldKind, want)
				}
			}
		})
	}
	for _, n := range []int64{0, 1, 127, 128, 16383, 16384, 1<<32 - 1, 1<<63 - 1} {
		var raw []byte
		if err := pool.QueryRow(t.Context(), `SELECT application_standard_restore_varint($1)`, n).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if want := protowire.AppendVarint(nil, uint64(n)); !bytes.Equal(raw, want) {
			t.Fatal("SQL varint differs from protobuf", n)
		}
	}
	for _, tc := range []struct{ kind, value string }{
		{"u32", "4294967296"}, {"i32", "2147483648"}, {"i64", "9223372036854775808"},
		{"i64", "-1"}, {"i64", "1.5"}, {"u32", "\"1\""}, {"bool", "1"}, {"string", "null"}, {"binding", "[]"},
	} {
		_, err := pool.Exec(t.Context(), `SELECT application_standard_restore_wire_field(1,$1,$2::jsonb)`, tc.kind, tc.value)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "application_standard_runtime_stale" {
			t.Fatal("SQL scalar was not refused", tc, err)
		}
	}
	for _, count := range []int{api.SidecarCapMax + 2, api.SidecarCapMax + 3} {
		drives := make([]map[string]any, count)
		for i := range drives {
			drives[i] = map[string]any{}
		}
		raw, err := json.Marshal(drives)
		if err != nil {
			t.Fatal(err)
		}
		_, err = pool.Exec(t.Context(), `SELECT application_standard_restore_wire_field(4,'drives',$1::jsonb)`, raw)
		if count == api.SidecarCapMax+2 && err != nil {
			t.Fatal("SQL refused maximum native drive count", err)
		}
		if count == api.SidecarCapMax+3 {
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
				t.Fatal("SQL exceeded central native drive cap", err)
			}
		}
	}
}
