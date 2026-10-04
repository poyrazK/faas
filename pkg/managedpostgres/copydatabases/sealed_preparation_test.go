// adr:566
package copydatabases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestCopyDatabasePreparationSealingRetainsOriginalPlanMappingAndTime(t *testing.T) {
	f := newFixture(t)
	r := f.prepare(t, f.ordinaryOID)
	key, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	sealed, err := SealPreparation(key.Recipient(), r)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenPreparation([]*age.X25519Identity{other, nil, key}, f.exports, f.plan, f.ordinaryOID, sealed)
	if err != nil || !recovered.CreatedAt().Equal(r.CreatedAt()) {
		t.Fatal("original preparation recovery", err)
	}
	a, _ := r.TargetForWorker()
	b, _ := recovered.TargetForWorker()
	if a != b || b.DatabaseOID == f.ordinaryOID {
		t.Fatal("recovery changed child identity or placement")
	}
	if err = recovered.VerifyForWorker(t.Context(), f.target, f.exports, f.authorize); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{sealed, recovered} {
		raw, _ := json.Marshal(value)
		for _, out := range []string{string(raw), fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value)} {
			if strings.Contains(out, f.ordinary) || strings.Contains(out, f.owner) || strings.Contains(out, "retained-private-stage-value") {
				t.Fatal("ordinary output exposed private preparation")
			}
		}
	}
	for _, fault := range []string{"missing key", "relabeled key", "scope", "owner", "provider", "fingerprint", "damage", "source mapping", "complete plan", "namespace", "version", "zero time", "future time", "target", "unknown field", "trailing input"} {
		t.Run(fault, func(t *testing.T) {
			s := sealed
			plan := f.plan
			oid := f.ordinaryOID
			ids := []*age.X25519Identity{other, key}
			want := pgerrors.ErrConflict
			switch fault {
			case "missing key":
				ids = []*age.X25519Identity{other}
				want = pgerrors.ErrUnavailable
			case "relabeled key":
				s.KeyID = other.Recipient().String()
			case "scope":
				s.Scope.SourceVersion = strings.Repeat("f", 64)
			case "owner":
				s.OwnerID = uuid.NewString()
			case "provider":
				s.ProviderResourceID = "different-independent-provider"
			case "fingerprint":
				s.Fingerprint = strings.Repeat("f", 64)
			case "damage":
				s.Ciphertext = bytes.Clone(s.Ciphertext)
				s.Ciphertext[len(s.Ciphertext)-1] ^= 1
				h := sha256.Sum256(s.Ciphertext)
				s.CiphertextSHA256 = hex.EncodeToString(h[:])
			case "source mapping":
				oid = f.templateOID
			case "complete plan":
				choices := append([]Disposition(nil), f.dispositions...)
				for n, d := range choices {
					if d.SourceOID == f.templateOID {
						choices[n].CreateTargetOID += 100
					}
				}
				var e error
				plan, e = NewPlan(f.exports, f.seed, f.baseline, choices, f.spaces)
				if e != nil {
					t.Fatal(e)
				}
			default:
				ns, raw, e := secretbox.OpenBytesMulti([]*age.X25519Identity{key}, sealed.Ciphertext)
				if e != nil {
					t.Fatal(e)
				}
				var body preparationEnvelope
				if json.Unmarshal(raw, &body) != nil {
					t.Fatal("decode private fixture")
				}
				switch fault {
				case "namespace":
					ns = "gregale-postgres-copy-target-pins-v1"
				case "version":
					body.Version = 2
				case "zero time":
					body.CreatedAt = time.Time{}
				case "future time":
					body.CreatedAt = time.Now().Add(time.Hour).Truncate(time.Microsecond)
				case "target":
					body.Target.DatabaseName += "different"
				}
				raw, _ = json.Marshal(body)
				if fault == "unknown field" {
					raw = append(raw[:len(raw)-1], []byte(`,"extra":true}`)...)
				}
				if fault == "trailing input" {
					raw = append(raw, []byte(` {}`)...)
				}
				s.Ciphertext, e = secretbox.SealBytes(key.Recipient(), ns, raw, api.PostgresCopyEnvelopeMaxBytes)
				if e != nil {
					t.Fatal(e)
				}
				h := sha256.Sum256(s.Ciphertext)
				s.CiphertextSHA256 = hex.EncodeToString(h[:])
			}
			if got, e := OpenPreparation(ids, f.exports, plan, oid, s); !errors.Is(e, want) || !got.CreatedAt().IsZero() {
				t.Fatalf("substituted preparation accepted: %v", e)
			}
		})
	}
	if _, err = SealPreparation(key.Recipient(), Receipt{}); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatal("empty receipt sealed", err)
	}
	oversized := sealed
	oversized.Ciphertext = bytes.Repeat([]byte{1}, api.PostgresCopyCiphertextMaxBytes+1)
	if _, err = OpenPreparation([]*age.X25519Identity{key}, f.exports, f.plan, f.ordinaryOID, oversized); !errors.Is(err, pgerrors.ErrQuotaExceeded) {
		t.Fatal("unbounded preparation", err)
	}
}

func TestCopyDatabaseRecoveredPreparationRequiresActualOriginalJournalWithoutRepair(t *testing.T) {
	for _, fault := range []string{"journal missing", "time changed", "state changed", "database missing", "catalogue drift", "stale after lock"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			r := f.prepare(t, f.ordinaryOID)
			key, _ := age.GenerateX25519Identity()
			s, err := SealPreparation(key.Recipient(), r)
			if err != nil {
				t.Fatal(err)
			}
			r, err = OpenPreparation([]*age.X25519Identity{key}, f.exports, f.plan, f.ordinaryOID, s)
			if err != nil {
				t.Fatal(err)
			}
			admin := f.targetRoot.Config().Copy()
			admin.Database = f.bootstrap
			c, err := pgx.ConnectConfig(t.Context(), admin)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close(context.Background())
			authorize := f.authorize
			switch fault {
			case "journal missing":
				run(t.Context(), t, c, "DROP SCHEMA gregale_copy_databases CASCADE")
			case "time changed":
				run(t.Context(), t, c, "UPDATE gregale_copy_databases.databases SET created_at=created_at+interval '1 second' WHERE source_oid=$1::oid", f.ordinaryOID)
			case "state changed":
				run(t.Context(), t, c, "UPDATE gregale_copy_databases.databases SET state='creating',created_at=NULL WHERE source_oid=$1::oid", f.ordinaryOID)
			case "database missing":
				run(t.Context(), t, f.targetRoot, "DROP DATABASE "+pgx.Identifier{f.ordinary}.Sanitize())
			case "catalogue drift":
				run(t.Context(), t, f.targetRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" CONNECTION LIMIT 19")
			case "stale after lock":
				var calls int
				authorize = func(ctx context.Context, target copyarchive.RestoreTarget) error {
					calls++
					if calls > 1 {
						return pgerrors.ErrConflict
					}
					return f.authorize(ctx, target)
				}
			}
			if err = r.VerifyForWorker(t.Context(), f.target, f.exports, authorize); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatalf("unqualified original journal accepted: %v", err)
			}
			if fault == "journal missing" {
				var exists bool
				if err = c.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_namespace WHERE nspname='gregale_copy_databases')").Scan(&exists); err != nil || exists {
					t.Fatal("verification recreated lost journal", err)
				}
			}
			if fault == "database missing" && f.exists(t.Context(), t, f.ordinary) {
				t.Fatal("verification recreated missing database")
			}
		})
	}
}
