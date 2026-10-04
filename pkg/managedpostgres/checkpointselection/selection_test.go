// adr:568
package checkpointselection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectionfence"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func fixture(t *testing.T) (*age.X25519Identity, Scope, managedpostgres.CheckpointConnectionRequest, [32]byte) {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), MaintenanceID: uuid.NewString(),
		SourceVersion: strings.Repeat("a", 64), BackendID: "private-backend", BackendFingerprint: strings.Repeat("b", 64), SourceProviderResourceID: "project-source", SourceDataResourceID: "project-source/br-source",
		PostgresMajor: 16, MaintenanceOwnerOID: 10001, MaintenanceDatabaseOID: 20002}
	r := managedpostgres.CheckpointConnectionRequest{CheckpointConnectionIdentity: managedpostgres.CheckpointConnectionIdentity{OwnerToken: scope.OperationID, SourceResourceID: scope.SourceDataResourceID},
		DatabaseNames: []string{"source_private_z\";ü%", "source_private_alpha"}}
	var key [32]byte
	copy(key[:], strings.Repeat("k", 32))
	return identity, scope, r, key
}

func TestSelectionCanonicalRecoveryKeepsOriginalScopeAndRotation(t *testing.T) {
	identity, scope, request, key := fixture(t)
	sealed, err := Seal(identity.Recipient(), scope, request, key)
	if err != nil {
		t.Fatal(err)
	}
	reordered := request
	reordered.DatabaseNames = slices.Clone(request.DatabaseNames)
	slices.Reverse(reordered.DatabaseNames)
	other, err := Seal(identity.Recipient(), scope, reordered, key)
	if err != nil || other.Fingerprint != sealed.Fingerprint {
		t.Fatalf("canonical selection: %v", err)
	}
	rotated, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	selection, err := Open([]*age.X25519Identity{nil, rotated, identity}, scope, sealed)
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(request.DatabaseNames)
	slices.Sort(want)
	r, err := selection.RequestForWorker(scope)
	if err != nil || r.OwnerToken != scope.OperationID || r.SourceResourceID != scope.SourceDataResourceID || !slices.Equal(r.DatabaseNames, want) {
		t.Fatalf("original request: %+v %v", r, err)
	}
	request.DatabaseNames[0] = "changed-after-seal"
	r.DatabaseNames[0] = "changed-after-open"
	actual, err := selection.RequestForWorker(scope)
	if err != nil || !slices.Equal(actual.DatabaseNames, want) {
		t.Fatalf("mutable selection: %+v %v", actual, err)
	}
	if _, err := Open([]*age.X25519Identity{rotated}, scope, sealed); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatalf("missing original key: %v", err)
	}
	key[0]++
	changed, err := Seal(identity.Recipient(), scope, reordered, key)
	if err != nil || changed.Fingerprint == sealed.Fingerprint {
		t.Fatalf("unkeyed selection fingerprint: %v", err)
	}
}

func TestSelectionRejectsInvalidScopeAndCallerSelection(t *testing.T) {
	identity, scope, request, key := fixture(t)
	for _, fault := range []string{"operation", "account", "project", "source", "maintenance", "same_owner", "same_source", "source_version", "backend", "backend_utf8", "backend_fingerprint", "provider", "data", "major", "owner_oid", "database_oid"} {
		x := scope
		switch fault {
		case "operation":
			x.OperationID = uuid.Nil.String()
		case "account":
			x.AccountID = "bad"
		case "project":
			x.ProjectID = "bad"
		case "source":
			x.SourceDatabaseID = "bad"
		case "maintenance":
			x.MaintenanceID = "bad"
		case "same_owner":
			x.MaintenanceID = x.OperationID
		case "same_source":
			x.MaintenanceID = x.SourceDatabaseID
		case "source_version":
			x.SourceVersion = strings.Repeat("A", 64)
		case "backend":
			x.BackendID = ""
		case "backend_utf8":
			x.BackendID = "bad\xff"
		case "backend_fingerprint":
			x.BackendFingerprint = "bad"
		case "provider":
			x.SourceProviderResourceID = "bad\x00"
		case "data":
			x.SourceDataResourceID = ""
		case "major":
			x.PostgresMajor = 15
		case "owner_oid":
			x.MaintenanceOwnerOID = 0
		case "database_oid":
			x.MaintenanceDatabaseOID = 0
		}
		actual, err := Seal(identity.Recipient(), x, request, key)
		if !errors.Is(err, pgerrors.ErrInvalid) || !reflect.DeepEqual(actual, Sealed{}) {
			t.Fatalf("invalid %s: %+v %v", fault, actual, err)
		}
	}
	for _, fault := range []string{"owner", "source", "empty", "duplicate", "name", "names", "maintenance_database", "key", "recipient"} {
		r, k, recipient := request, key, identity.Recipient()
		r.DatabaseNames = slices.Clone(request.DatabaseNames)
		want := pgerrors.ErrInvalid
		switch fault {
		case "owner":
			r.OwnerToken = uuid.NewString()
			want = pgerrors.ErrConflict
		case "source":
			r.SourceResourceID += "-other"
			want = pgerrors.ErrConflict
		case "empty":
			r.DatabaseNames = nil
		case "duplicate":
			r.DatabaseNames[0] = r.DatabaseNames[1]
		case "name":
			r.DatabaseNames[0] = strings.Repeat("ü", 32)
		case "names":
			r.DatabaseNames = make([]string, api.PostgresCheckpointDatabasesMax+1)
		case "maintenance_database":
			r.DatabaseNames[0] = connectionfence.MaintenanceDatabase
		case "key":
			k = [32]byte{}
		case "recipient":
			recipient = nil
		}
		actual, err := Seal(recipient, scope, r, k)
		if !errors.Is(err, want) || !reflect.DeepEqual(actual, Sealed{}) {
			t.Fatalf("invalid %s selection: %+v %v", fault, actual, err)
		}
	}
}

func TestSelectionNeverRebasesDamagedOrSubstitutedMetadata(t *testing.T) {
	identity, scope, request, key := fixture(t)
	sealed, err := Seal(identity.Recipient(), scope, request, key)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"scope", "fingerprint", "key_id", "wrong_recipient", "ciphertext", "cipher_hash", "oversize", "header_scope"} {
		x, expected := sealed, scope
		x.Ciphertext = bytes.Clone(sealed.Ciphertext)
		switch fault {
		case "scope":
			expected.MaintenanceOwnerOID++
		case "fingerprint":
			x.Fingerprint = strings.Repeat("c", 64)
		case "key_id":
			x.KeyID = "bad"
		case "wrong_recipient":
			x.KeyID = otherKey.Recipient().String()
		case "ciphertext":
			x.Ciphertext[len(x.Ciphertext)-1] ^= 1
		case "cipher_hash":
			x.CiphertextSHA256 = strings.Repeat("d", 64)
		case "oversize":
			x.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
		case "header_scope":
			expected.MaintenanceDatabaseOID++
			x.Scope = expected
		}
		actual, err := Open([]*age.X25519Identity{identity, otherKey}, expected, x)
		if err == nil || !reflect.DeepEqual(actual, Selection{}) {
			t.Fatalf("damaged %s opened metadata: %+v %v", fault, actual, err)
		}
	}
}

func TestSelectionRejectsMalformedPrivateEnvelopes(t *testing.T) {
	identity, scope, request, key := fixture(t)
	sealed, err := Seal(identity.Recipient(), scope, request, key)
	if err != nil {
		t.Fatal(err)
	}
	_, original, err := secretbox.OpenBytesMulti([]*age.X25519Identity{identity}, sealed.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"version", "scope", "key", "fingerprint", "unsorted", "duplicate", "private_database", "unknown", "trailing", "namespace", "oversize"} {
		var body envelope
		if err := json.Unmarshal(original, &body); err != nil {
			t.Fatal(err)
		}
		switch fault {
		case "version":
			body.Payload.Version++
		case "scope":
			body.Payload.Scope.AccountID = uuid.NewString()
		case "key":
			body.Key = [32]byte{}
		case "fingerprint":
			body.Fingerprint = strings.Repeat("e", 64)
		case "unsorted":
			slices.Reverse(body.Payload.DatabaseNames)
		case "duplicate":
			body.Payload.DatabaseNames[0] = body.Payload.DatabaseNames[1]
		case "private_database":
			body.Payload.DatabaseNames = []string{"gregale_checkpoint"}
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		ns := namespace
		switch fault {
		case "unknown":
			raw = append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
		case "trailing":
			raw = append(raw, []byte(` {}`)...)
		case "namespace":
			ns += "-other"
		case "oversize":
			raw = bytes.Repeat([]byte("x"), api.PostgresCopyEnvelopeMaxBytes+1)
		}
		ciphertext, err := secretbox.SealBytes(identity.Recipient(), ns, raw, len(raw))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(ciphertext)
		x := sealed
		x.Ciphertext = ciphertext
		x.CiphertextSHA256 = hex.EncodeToString(hash[:])
		actual, err := Open([]*age.X25519Identity{identity}, scope, x)
		if err == nil || !reflect.DeepEqual(actual, Selection{}) {
			t.Fatalf("malformed %s produced selection: %+v %v", fault, actual, err)
		}
	}
}

func TestSelectionOrdinaryOutputRedactsNamesKeyAndOwner(t *testing.T) {
	identity, scope, request, key := fixture(t)
	sealed, err := Seal(identity.Recipient(), scope, request, key)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := Open([]*age.X25519Identity{identity}, scope, sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{selection, sealed} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		for _, rendered := range []string{string(raw), fmt.Sprint(value), fmt.Sprintf("%#v", value), fmt.Sprintf("%+v", value)} {
			for _, private := range append(slices.Clone(request.DatabaseNames), scope.OperationID, scope.SourceDataResourceID, string(key[:])) {
				if strings.Contains(rendered, private) {
					t.Fatal("ordinary output exposed private selection")
				}
			}
		}
	}
}
