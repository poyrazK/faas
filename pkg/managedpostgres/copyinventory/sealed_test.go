// adr:568
package copyinventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestSealedInventoryRejectsRelabeledRecipientWithBothRotationKeysAvailable(t *testing.T) {
	cfg, inventory := sealedInventoryValue(t)
	scope := sealedInventoryScope(cfg.PostgresMajor)
	original, _ := age.GenerateX25519Identity()
	current, _ := age.GenerateX25519Identity()
	sealed, err := SealInventory(original.Recipient(), scope, cfg, inventory)
	if err != nil {
		t.Fatal(err)
	}
	identities := []*age.X25519Identity{current, original}
	if _, err = OpenInventory(identities, scope, sealed); err != nil {
		t.Fatalf("original recipient recovery: %v", err)
	}
	relabeled := sealed
	relabeled.KeyID = current.Recipient().String()
	if _, err = OpenInventory(identities, scope, relabeled); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("substituted recipient borrowed another rotation key: %v", err)
	}
}

func sealedInventoryScope(major int) Scope {
	at := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	return Scope{PostgresMajor: major, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(),
		SourceVersion: strings.Repeat("a", 64), BackendID: "neon", BackendFingerprint: strings.Repeat("b", 64), SourceProviderResourceID: "project-source",
		SourceDataResourceID: "project-source/br-source", ProviderSnapshotID: "sn-owned", CaptureProviderResourceID: "project-source/br-capture",
		CapturePoint: at, SnapshotCreatedAt: at.Add(time.Second), CaptureCreatedAt: at.Add(2 * time.Second)}
}

func sealedInventoryValue(t *testing.T) (Config, Inventory) {
	t.Helper()
	cfg := Config{PostgresMajor: 16, DatabaseName: "private_database", DatabaseOID: 2, RoleName: "private_role", RoleOID: 1}
	cfg.FingerprintKey[0] = 7
	b := payload{Version: 1, PostgresMajor: 16, DatabaseOID: 2, RoleOID: 1,
		Databases:   []Database{{OID: 2, Name: cfg.DatabaseName, OwnerOID: 1, Owner: cfg.RoleName, TablespaceOID: 3}},
		Roles:       []Role{{OID: 1, Name: cfg.RoleName, Config: []string{"app.api_key=private-config-secret"}}},
		Tablespaces: []Tablespace{{OID: 3, Name: "pg_default", OwnerOID: 1, Owner: cfg.RoleName}},
		Memberships: []Membership{}, Settings: []Setting{}, PreparedTransactions: []PreparedTransaction{}}
	if !validPayload(b) {
		t.Fatal("invalid test inventory")
	}
	fingerprint, err := fingerprintPayload(b, cfg.FingerprintKey)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, Inventory{body: b, fingerprint: hex.EncodeToString(fingerprint)}
}

func TestSealedInventoryReadsAndRecoversOriginalSQLWithRotation(t *testing.T) {
	f := newInventoryFixture(t)
	inventory, err := Read(t.Context(), f.conn, f.cfg)
	if err != nil {
		t.Fatal(err)
	}
	previous, _ := age.GenerateX25519Identity()
	current, _ := age.GenerateX25519Identity()
	scope := sealedInventoryScope(f.cfg.PostgresMajor)
	sealed, err := SealInventory(previous.Recipient(), scope, f.cfg, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.conn.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := OpenInventory([]*age.X25519Identity{current, nil, previous}, scope, sealed)
	if err != nil || recovered.Summary() != inventory.Summary() {
		t.Fatalf("rotation recovery: %v", err)
	}
	original, _ := inventory.PayloadForSealing()
	raw, _ := recovered.PayloadForSealing()
	if !bytes.Equal(raw, original) {
		t.Fatal("private catalogue changed during sealed recovery")
	}
	output, err := json.Marshal(sealed)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{f.cfg.DatabaseName, f.cfg.RoleName, "source-secret-setting", "password-never-in-inventory"} {
		for _, output := range []string{string(output), string(sealed.Ciphertext), fmt.Sprintf("%+v %#v", sealed, sealed)} {
			if strings.Contains(output, private) {
				t.Fatal("sealed metadata leaked a private name/value")
			}
		}
	}
	if _, err := OpenInventory([]*age.X25519Identity{current}, scope, sealed); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatalf("missing previous identity: %v", err)
	}
}

func TestSealedInventoryRejectsEveryScopeSubstitution(t *testing.T) {
	cfg, inventory := sealedInventoryValue(t)
	identity, _ := age.GenerateX25519Identity()
	scope := sealedInventoryScope(cfg.PostgresMajor)
	sealed, err := SealInventory(identity.Recipient(), scope, cfg, inventory)
	if err != nil {
		t.Fatal(err)
	}
	// Replace both outer scope and caller expectations. The original encrypted
	// scope must still reject every otherwise-valid cross-operation substitution.
	for field := 0; field < reflect.TypeOf(scope).NumField(); field++ {
		name := reflect.TypeOf(scope).Field(field).Name
		t.Run(name, func(t *testing.T) {
			changed := scope
			value := reflect.ValueOf(&changed).Elem().Field(field)
			switch value.Kind() {
			case reflect.String:
				value.SetString(strings.ReplaceAll(value.String(), "a", "c") + "x")
				if strings.HasSuffix(name, "ID") && name != "BackendID" && !strings.Contains(name, "Provider") && name != "SourceDataResourceID" {
					value.SetString(uuid.NewString())
				}
				if name == "SourceVersion" || name == "BackendFingerprint" {
					value.SetString(strings.Repeat("c", 64))
				}
			case reflect.Int:
				value.SetInt(value.Int() + 1)
			default:
				value.Set(reflect.ValueOf(value.Interface().(time.Time).Add(time.Microsecond)))
			}
			if changed.Validate() != nil {
				t.Fatal("invalid scope substitution fixture")
			}
			copy := sealed
			copy.Scope = changed
			if _, err := OpenInventory([]*age.X25519Identity{identity}, changed, copy); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatalf("scope substitution accepted: %v", err)
			}
		})
	}
	local := scope
	local.CapturePoint = local.CapturePoint.In(time.FixedZone("equivalent", 3600))
	if _, err := OpenInventory([]*age.X25519Identity{identity}, local, sealed); err != nil {
		t.Fatalf("same instant in another zone: %v", err)
	}
}

func TestSealedInventoryRejectsTamperingMalformedEnvelopesAndOversize(t *testing.T) {
	cfg, inventory := sealedInventoryValue(t)
	identity, _ := age.GenerateX25519Identity()
	scope := sealedInventoryScope(cfg.PostgresMajor)
	sealed, err := SealInventory(identity.Recipient(), scope, cfg, inventory)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := secretbox.OpenBytes(identity, sealed.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"namespace", "version", "unknown", "unknown_scope", "trailing", "fingerprint", "key", "identity", "major", "payload", "ciphertext"} {
		t.Run(mode, func(t *testing.T) {
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			namespace := sealedNamespace
			switch mode {
			case "namespace":
				namespace = "different-purpose"
			case "version":
				body["version"] = 2
			case "unknown":
				body["unrecognized"] = true
			case "unknown_scope":
				body["scope"].(map[string]any)["unrecognized"] = true
			case "fingerprint":
				body["fingerprint"] = strings.Repeat("c", 64)
			case "key":
				body["config"].(map[string]any)["FingerprintKey"].([]any)[0] = float64(8)
			case "identity":
				body["config"].(map[string]any)["DatabaseOID"] = float64(9)
			case "major":
				body["config"].(map[string]any)["PostgresMajor"] = float64(17)
			case "payload":
				body["payload"].(map[string]any)["roles"].([]any)[0].(map[string]any)["name"] = "changed_role"
			}
			modified, _ := json.Marshal(body)
			if mode == "trailing" {
				modified = append(modified, []byte("{}")...)
			}
			copy := sealed
			copy.Ciphertext, err = secretbox.SealBytes(identity.Recipient(), namespace, modified, api.PostgresCopyEnvelopeMaxBytes)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "ciphertext" {
				copy.Ciphertext[len(copy.Ciphertext)-1] ^= 1
			}
			hash := sha256.Sum256(copy.Ciphertext)
			copy.CiphertextSHA256 = hex.EncodeToString(hash[:])
			if _, err := OpenInventory([]*age.X25519Identity{identity}, scope, copy); !errors.Is(err, pgerrors.ErrConflict) {
				t.Fatalf("malformed envelope accepted: %v", err)
			}
		})
	}
	inventory.body.Roles[0].Config = []string{strings.Repeat("x", api.PostgresCopyInventoryMaxBytes)}
	if _, err := SealInventory(identity.Recipient(), scope, cfg, inventory); !errors.Is(err, pgerrors.ErrQuotaExceeded) {
		t.Fatalf("oversized metadata truncated: %v", err)
	}
	sealed.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1)
	if _, err := OpenInventory([]*age.X25519Identity{identity}, scope, sealed); !errors.Is(err, pgerrors.ErrQuotaExceeded) {
		t.Fatalf("oversized ciphertext accepted: %v", err)
	}
}

// adr:568
func TestScopeJSONCanonicalizesNumericUTCTimestamps(t *testing.T) {
	previous := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = previous })
	want := sealedInventoryScope(16)
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, encoded := range []string{string(raw), strings.ReplaceAll(string(raw), "Z\"", "+00:00\"")} {
		var got Scope
		if err := json.Unmarshal([]byte(encoded), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("scope changed across JSON timestamp representation: got %#v, want %#v", got, want)
		}
	}
	var got Scope
	unknown := strings.TrimSuffix(string(raw), "}") + ",\"unexpected\":true}"
	if err := json.Unmarshal([]byte(unknown), &got); err == nil {
		t.Fatal("scope accepted an unknown authenticated field")
	}
}
