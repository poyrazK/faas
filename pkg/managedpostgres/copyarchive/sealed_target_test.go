// adr:566
package copyarchive

import (
	"bytes"
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
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func sealedTargetFixture(t *testing.T) (RestoreTarget, *age.X25519Identity) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	point := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	scope := copyinventory.Scope{PostgresMajor: 16, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(), SourceVersion: strings.Repeat("a", 64), BackendID: "private-neon", BackendFingerprint: strings.Repeat("b", 64), SourceProviderResourceID: "source-project", SourceDataResourceID: "source-project/br-live", ProviderSnapshotID: "source-project/snapshots/snap-owned", CaptureProviderResourceID: "source-project/br-capture", CapturePoint: point, SnapshotCreatedAt: point.Add(time.Second), CaptureCreatedAt: point.Add(2 * time.Second)}
	return RestoreTarget{Scope: scope, OwnerID: uuid.NewString(), ProviderResourceID: "independent-project", DataResourceID: "independent-project/br-root", EndpointID: "ep-independent", ProviderCreatedAt: point.Add(3 * time.Second), EndpointCreatedAt: point.Add(4 * time.Second), DatabaseName: "private /?%&数据库\n", DatabaseOID: 17001, RoleName: "private target role", RoleOID: 17002}, id
}

func TestSealedTargetRetainsExactPrivatePinsAndOriginalKeyAcrossRotation(t *testing.T) {
	target, id := sealedTargetFixture(t)
	sealed, err := SealTarget(id.Recipient(), target)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := age.GenerateX25519Identity()
	actual, err := OpenTarget([]*age.X25519Identity{other, nil, id}, target.Scope, sealed)
	if err != nil || actual != target {
		t.Fatalf("sealed target recovery: %v", err)
	}
	raw, _ := json.Marshal(sealed)
	for _, value := range []string{string(raw), fmt.Sprint(sealed), fmt.Sprintf("%#v", sealed)} {
		if strings.Contains(value, target.DatabaseName) || strings.Contains(value, target.RoleName) || bytes.Contains(sealed.Ciphertext, []byte(target.DatabaseName)) {
			t.Fatal("sealed target exposed private SQL names")
		}
	}
	// Equivalent instants may have different zones; immutable digests normalize
	// all timestamps and the decrypted descriptor retains original exact pins.
	zoned := target
	zoned.ProviderCreatedAt = zoned.ProviderCreatedAt.In(time.FixedZone("other", 3600))
	zoned.Scope.CapturePoint = zoned.Scope.CapturePoint.In(time.FixedZone("other", 3600))
	a, _ := target.Fingerprint()
	b, _ := zoned.Fingerprint()
	if a != b {
		t.Fatal("timestamp zone changed target fingerprint")
	}
	if _, err := OpenTarget([]*age.X25519Identity{other}, target.Scope, sealed); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatalf("missing original key: %v", err)
	}
}

func TestSealedTargetRejectsReceiptScopeOwnerAndCiphertextSubstitution(t *testing.T) {
	for _, fault := range []string{"scope", "owner", "project", "project_time", "fingerprint", "key", "ciphertext", "truncated", "empty", "hash", "oversized"} {
		t.Run(fault, func(t *testing.T) {
			target, id := sealedTargetFixture(t)
			sealed, err := SealTarget(id.Recipient(), target)
			if err != nil {
				t.Fatal(err)
			}
			other, _ := age.GenerateX25519Identity()
			expected := target.Scope
			want := error(pgerrors.ErrConflict)
			switch fault {
			case "scope":
				expected.OperationID = uuid.NewString()
			case "owner":
				sealed.OwnerID = uuid.NewString()
			case "project":
				sealed.ProviderResourceID = "other-project"
			case "project_time":
				sealed.ProviderCreatedAt = sealed.ProviderCreatedAt.Add(time.Microsecond)
			case "fingerprint":
				sealed.Fingerprint = strings.Repeat("f", 64)
			case "key":
				sealed.KeyID = other.Recipient().String()
			case "ciphertext":
				sealed.Ciphertext[len(sealed.Ciphertext)-1] ^= 1
				h := sha256.Sum256(sealed.Ciphertext)
				sealed.CiphertextSHA256 = hex.EncodeToString(h[:])
			case "truncated":
				sealed.Ciphertext = sealed.Ciphertext[:len(sealed.Ciphertext)-1]
				h := sha256.Sum256(sealed.Ciphertext)
				sealed.CiphertextSHA256 = hex.EncodeToString(h[:])
			case "empty":
				sealed.Ciphertext = nil
				want = pgerrors.ErrInvalid
			case "hash":
				sealed.CiphertextSHA256 = "invalid"
				want = pgerrors.ErrInvalid
			case "oversized":
				sealed.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
				want = pgerrors.ErrQuotaExceeded
			}
			actual, err := OpenTarget([]*age.X25519Identity{id, other}, expected, sealed)
			if !errors.Is(err, want) || actual != (RestoreTarget{}) {
				t.Fatalf("substituted target recovered: %v", err)
			}
		})
	}
}

func TestSealedTargetRejectsWrongNamespaceMalformedEnvelopeAndInnerPins(t *testing.T) {
	for _, fault := range []string{"namespace", "version", "unknown", "trailing", "database", "role_oid", "endpoint", "inner_scope", "envelope_budget"} {
		t.Run(fault, func(t *testing.T) {
			target, id := sealedTargetFixture(t)
			sealed, err := SealTarget(id.Recipient(), target)
			if err != nil {
				t.Fatal(err)
			}
			namespace := sealedTargetNamespace
			envelope := privateTargetEnvelope{Version: 1, Target: privateTargetPins(target)}
			want := error(pgerrors.ErrConflict)
			switch fault {
			case "namespace":
				namespace = "gregale-postgres-copy-inventory-v1"
			case "version":
				envelope.Version = 2
			case "database":
				envelope.Target.DatabaseName = "another-private-database"
			case "role_oid":
				envelope.Target.RoleOID++
			case "endpoint":
				envelope.Target.EndpointID = "ep-other"
			case "inner_scope":
				envelope.Target.Scope.OperationID = uuid.NewString()
			}
			raw, _ := json.Marshal(envelope)
			if fault == "unknown" {
				raw = append(raw[:len(raw)-1], []byte(",\"unknown\":true}")...)
			}
			if fault == "trailing" {
				raw = append(raw, []byte(" {}")...)
			}
			if fault == "envelope_budget" {
				raw = make([]byte, api.PostgresCopyEnvelopeMaxBytes+1)
				want = pgerrors.ErrQuotaExceeded
			}
			sealed.Ciphertext, err = secretbox.SealBytes(id.Recipient(), namespace, raw, len(raw))
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(sealed.Ciphertext)
			sealed.CiphertextSHA256 = hex.EncodeToString(h[:])
			actual, err := OpenTarget([]*age.X25519Identity{id}, target.Scope, sealed)
			if !errors.Is(err, want) || actual != (RestoreTarget{}) {
				t.Fatalf("invalid encrypted target pins recovered: %v", err)
			}
		})
	}
	if _, err := SealTarget(nil, RestoreTarget{}); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("nil target recipient: %v", err)
	}
}
