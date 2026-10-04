// adr:566
package copydatabases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

func TestCopyDatabaseContentsRetainedMatchBindsOriginalWindowCiphertextAndClosure(t *testing.T) {
	f, preparation, d, source, cfg := contentsFixture(t, "")
	manifest, err := copycontents.Capture(t.Context(), source, d, cfg, contentsSourcePlacement(f, source, d))
	if err != nil {
		t.Fatal(err)
	}
	imported := contentsRestore(t, f, preparation, d, source)
	owner := uuid.New()
	key, _ := age.GenerateX25519Identity()
	rotated, _ := age.GenerateX25519Identity()
	var sealed copycontents.SealedMatch
	var target copyarchive.RestoreTarget
	var readMatch copycontents.Match
	closure, err := preparation.WithVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize,
		func(ctx context.Context, access VerificationTarget) error {
			var err error
			target, err = access.TargetForWorker()
			if err != nil {
				return err
			}
			binding, err := access.IdentityForWorker()
			if err != nil || binding.OwnerID != owner || binding.ImportID != imported {
				return pgerrors.ErrConflict
			}
			child := maintenanceChild(ctx, t, f, target)
			defer child.Close(context.WithoutCancel(ctx))
			err = access.WithReadOnly(ctx, child, verificationPlacement(f, child, target), func(ctx context.Context, tx pgx.Tx) error {
				var err error
				readMatch, err = manifest.CompareTarget(ctx, tx, target, cfg, verificationPlacement(f, child, target))
				return err
			})
			if err != nil {
				return err
			}
			sealed, err = copycontents.SealMatch(key.Recipient(), manifest, target, owner, imported, binding.OpenedAt, readMatch)
			return err
		})
	if err != nil || !closure.MatchesForWorker(preparation, imported, owner, sealed.OpenedAt) {
		t.Fatal("actual comparison/window binding", err)
	}
	retained, err := copycontents.OpenMatch([]*age.X25519Identity{rotated, nil, key}, manifest, target, owner, imported, sealed)
	if err != nil || !retained.Matches(manifest, target, sealed) {
		t.Fatal("original retained match recovery", err)
	}
	if _, err := copycontents.OpenMatch([]*age.X25519Identity{rotated}, manifest, target, owner, imported, sealed); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatal("current key substituted original recipient", err)
	}
	again, err := copycontents.SealMatch(key.Recipient(), manifest, target, owner, imported, sealed.OpenedAt, readMatch)
	if err != nil || bytes.Equal(again.Ciphertext, sealed.Ciphertext) || retained.Matches(manifest, target, again) {
		t.Fatal("retained capability adopted equivalent re-encryption", err)
	}
	for _, mode := range []string{"scope", "source_oid", "target_oid", "owner", "import", "opened", "manifest", "target", "recipient", "fingerprint", "ciphertext", "oversized", "public_recipient_forgery"} {
		t.Run(mode, func(t *testing.T) {
			input := sealed
			input.Ciphertext = bytes.Clone(sealed.Ciphertext)
			switch mode {
			case "scope":
				input.Scope.OperationID = uuid.NewString()
			case "source_oid":
				input.SourceDatabaseOID++
			case "target_oid":
				input.TargetDatabaseOID++
			case "owner":
				input.OwnerID = uuid.NewString()
			case "import":
				input.ImportID = uuid.NewString()
			case "opened":
				input.OpenedAt = input.OpenedAt.Add(time.Microsecond)
			case "manifest":
				input.ManifestFingerprint = strings.Repeat("e", 64)
			case "target":
				input.TargetFingerprint = strings.Repeat("f", 64)
			case "recipient":
				input.KeyID = rotated.Recipient().String()
			case "fingerprint":
				input.Fingerprint = strings.Repeat("d", 64)
			case "ciphertext":
				input.Ciphertext[len(input.Ciphertext)-1] ^= 1
			case "oversized":
				input.Ciphertext = make([]byte, api.PostgresCopyVerificationCiphertextMaxBytes+1)
			case "public_recipient_forgery":
				// Correct age encryption and headers do not replace the MAC held
				// only by the original private source manifest.
				ns, raw, err := secretbox.OpenBytesMulti([]*age.X25519Identity{key}, sealed.Ciphertext)
				if err != nil {
					t.Fatal(err)
				}
				var body map[string]any
				if err := json.Unmarshal(raw, &body); err != nil {
					t.Fatal(err)
				}
				input.OpenedAt = input.OpenedAt.Add(time.Microsecond)
				body["OpenedAt"] = input.OpenedAt
				raw, _ = json.Marshal(body)
				input.Ciphertext, err = secretbox.SealBytes(key.Recipient(), ns, raw, api.PostgresCopyVerificationEnvelopeMaxBytes)
				if err != nil {
					t.Fatal(err)
				}
			}
			h := sha256.Sum256(input.Ciphertext)
			input.CiphertextSHA256 = hex.EncodeToString(h[:])
			got, err := copycontents.OpenMatch([]*age.X25519Identity{key, rotated}, manifest, target, owner, imported, input)
			if err == nil || !reflect.DeepEqual(got, copycontents.RetainedMatch{}) || retained.Matches(manifest, target, input) {
				t.Fatal("substitution manufactured retained data proof", err)
			}
		})
	}
	if closure.MatchesForWorker(preparation, imported, uuid.New(), sealed.OpenedAt) || closure.MatchesForWorker(preparation, uuid.New(), owner, sealed.OpenedAt) ||
		closure.MatchesForWorker(preparation, imported, owner, sealed.OpenedAt.Add(time.Microsecond)) || (VerificationClosure{}).MatchesForWorker(preparation, imported, owner, sealed.OpenedAt) {
		t.Fatal("closure adopted foreign owner/import/window or zero proof")
	}
	replayed, err := preparation.CloseVerificationAccess(t.Context(), f.target, f.exports, imported, owner, f.authorize)
	if err != nil || !replayed.ClosedAt().Equal(closure.ClosedAt()) || !replayed.MatchesForWorker(preparation, imported, owner, sealed.OpenedAt) {
		t.Fatal("native close-only replay changed first timestamp", err)
	}
	for _, value := range []any{sealed, retained} {
		raw, _ := json.Marshal(value)
		for _, display := range []string{fmtDisplay(value), string(raw)} {
			if strings.Contains(display, d.Database.Name) || strings.Contains(display, d.Scope.OperationID) || strings.Contains(display, sealed.OwnerID) {
				t.Fatal("private verification proof escaped formatted/JSON output")
			}
		}
	}
	assertMaintenanceOriginal(t, f, preparation)
}
