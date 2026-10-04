// adr: 581
package copyroles

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestRoleSeedPlanRequiresEveryExactDispositionAndDeepCopiesPrivateCatalogue(t *testing.T) {
	f := newFixture(t)
	p := f.plan(t)
	for _, tc := range []struct {
		name    string
		choices func() []Disposition
		want    error
	}{
		{"missing_role", func() []Disposition { return slices.Clone(f.dispositions[1:]) }, pgerrors.ErrConflict},
		{"duplicate_source", func() []Disposition { d := slices.Clone(f.dispositions); d[0] = d[1]; return d }, pgerrors.ErrConflict},
		{"foreign_source", func() []Disposition { d := slices.Clone(f.dispositions); d[0].SourceOID = 4000000000; return d }, pgerrors.ErrConflict},
		{"name_adoption", func() []Disposition {
			d := slices.Clone(f.dispositions)
			for n := range d {
				if d[n].ExistingTargetOID == f.pins.RoleOID {
					d[n].ExistingTargetOID = 0
				}
			}
			return d
		}, pgerrors.ErrConflict},
		{"wrong_existing_role", func() []Disposition {
			d := slices.Clone(f.dispositions)
			for n := range d {
				if d[n].ExistingTargetOID == 0 {
					d[n].ExistingTargetOID = f.pins.RoleOID
					break
				}
			}
			return d
		}, pgerrors.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewPlan(f.sourcePlan, f.targetInventory, f.pins, tc.choices()); !errors.Is(err, tc.want) {
				t.Fatalf("invalid classification: %v", err)
			}
		})
	}
	before, _ := p.PrivatePayloadForSealing()
	catalogue, err := f.sourcePlan.RoleCatalogueForWorker()
	if err != nil {
		t.Fatal(err)
	}
	for n := range catalogue.Roles {
		if catalogue.Roles[n].Name == f.member {
			catalogue.Roles[n].Name = "changed"
			catalogue.Roles[n].Config[0] = "app.api_key=changed"
		}
	}
	after, _ := p.PrivatePayloadForSealing()
	if string(before) != string(after) {
		t.Fatal("returned worker catalogue changed immutable plan")
	}
	src, _ := f.sourcePlan.RoleCatalogueForWorker()
	for _, r := range src.Roles {
		if r.Name == "changed" || slices.Contains(r.Config, "app.api_key=changed") {
			t.Fatal("worker catalogue aliases retained source inventory")
		}
	}
	// A caller mutation of classification input cannot change the committed plan.
	f.dispositions[0].ExistingTargetOID++
	if after, _ = p.PrivatePayloadForSealing(); string(before) != string(after) {
		t.Fatal("mutable disposition aliases private plan")
	}
	key, _ := age.GenerateX25519Identity()
	sealed, err := Seal(key.Recipient(), p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open([]*age.X25519Identity{key}, f.sourcePlan, f.pins, sealed); err != nil {
		t.Fatal(err)
	}
	if _, err = Prepare(t.Context(), f.target, Plan{}, allow); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatalf("empty plan dispatched: %v", err)
	}
}

func TestRoleSeedSealingRejectsKeyScopeNamespaceEnvelopeAndSourceRebasing(t *testing.T) {
	f := newFixture(t)
	p := f.plan(t)
	key, _ := age.GenerateX25519Identity()
	other, _ := age.GenerateX25519Identity()
	sealed, err := Seal(key.Recipient(), p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Sealed)
		want error
	}{
		{"key_substitution", func(s *Sealed) { s.KeyID = other.Recipient().String() }, pgerrors.ErrConflict},
		{"scope_substitution", func(s *Sealed) { s.Scope.SourceVersion = strings.Repeat("f", 64) }, pgerrors.ErrConflict},
		{"source_fingerprint", func(s *Sealed) { s.InventoryFingerprint = strings.Repeat("f", 64) }, pgerrors.ErrConflict},
		{"target_fingerprint", func(s *Sealed) { s.TargetFingerprint = strings.Repeat("f", 64) }, pgerrors.ErrConflict},
		{"ciphertext_hash", func(s *Sealed) { s.CiphertextSHA256 = strings.Repeat("f", 64) }, pgerrors.ErrConflict},
		{"ciphertext_budget", func(s *Sealed) { s.Ciphertext = make([]byte, api.PostgresCopyCiphertextMaxBytes+1) }, pgerrors.ErrQuotaExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sealed
			tc.edit(&s)
			if _, err := Open([]*age.X25519Identity{other, key}, f.sourcePlan, f.pins, s); !errors.Is(err, tc.want) {
				t.Fatalf("substituted private receipt: %v", err)
			}
		})
	}
	if _, err = Open([]*age.X25519Identity{other}, f.sourcePlan, f.pins, sealed); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatalf("missing original key: %v", err)
	}
	raw, err := p.PrivatePayloadForSealing()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, namespace string
		edit            func([]byte) []byte
		want            error
	}{
		{"namespace", "wrong-role-namespace", func(b []byte) []byte { return b }, pgerrors.ErrConflict},
		{"unknown_field", sealedNamespace, func(b []byte) []byte { return append(slices.Clone(b[:len(b)-1]), []byte(`,"unexpected":true}`)...) }, pgerrors.ErrConflict},
		{"trailing_payload", sealedNamespace, func(b []byte) []byte { return append(slices.Clone(b), []byte(` {}`)...) }, pgerrors.ErrConflict},
		{"source_metadata", sealedNamespace, func(b []byte) []byte {
			var x payload
			_ = json.Unmarshal(b, &x)
			for n := range x.Roles {
				if x.Roles[n].Source.Name == f.member {
					x.Roles[n].Source.Login = false
				}
			}
			v, _ := json.Marshal(x)
			return v
		}, pgerrors.ErrConflict},
		{"target_sql_oid", sealedNamespace, func(b []byte) []byte {
			var x payload
			_ = json.Unmarshal(b, &x)
			x.Target.DatabaseOID++
			v, _ := json.Marshal(x)
			return v
		}, pgerrors.ErrInvalid},
		{"decrypted_budget", sealedNamespace, func(b []byte) []byte {
			return append(slices.Clone(b), []byte(strings.Repeat(" ", api.PostgresCopyEnvelopeMaxBytes))...)
		}, pgerrors.ErrQuotaExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sealed
			v := tc.edit(raw)
			cipher, err := secretbox.SealBytes(key.Recipient(), tc.namespace, v, len(v)+1)
			if err != nil {
				t.Fatal(err)
			}
			s.Ciphertext = cipher
			h := sha256.Sum256(cipher)
			s.CiphertextSHA256 = hex.EncodeToString(h[:])
			_, err = Open([]*age.X25519Identity{key}, f.sourcePlan, f.pins, s)
			if !errors.Is(err, tc.want) {
				t.Fatalf("invalid private payload accepted: %v", err)
			}
		})
	}
	// Real source changes cannot replace the frozen plan and its fingerprint.
	if _, err = f.sourceRoot.Exec(t.Context(), "ALTER ROLE "+pgx.Identifier{f.group}.Sanitize()+" CONNECTION LIMIT 3"); err != nil {
		t.Fatal(err)
	}
	f.refreshSource(t)
	if _, err = Open([]*age.X25519Identity{key}, f.sourcePlan, f.pins, sealed); !errors.Is(err, pgerrors.ErrConflict) {
		t.Fatalf("today's source rebased the original role plan: %v", err)
	}
	var first, second copyinventory.RoleCatalogue
	first, _ = f.sourcePlan.RoleCatalogueForWorker()
	second, _ = f.sourcePlan.RoleCatalogueForWorker()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("source role catalogue recovery is unstable")
	}
}
