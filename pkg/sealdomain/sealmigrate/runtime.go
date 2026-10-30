package sealmigrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// RuntimeStore is the slice of *state.PgStore the runtime re-seal uses: the
// release-secret rekey documents (ADR-1139). They cover every stored runtime
// secret, one document per row: app and project environment secrets, staged
// release bundles, deployment snapshots, and release and promotion captures.
// The compare-and-swap rewrites only kid and ciphertext, so no runtime-change
// trigger fires and no immutable snapshot changes meaning.
type RuntimeStore interface {
	ListSecretRekeyDocuments(ctx context.Context, kid, after string, limit int) ([]state.SecretRekeyDocument, error)
	CompareAndSwapSecretRekeyDocument(ctx context.Context, before state.SecretRekeyDocument, after json.RawMessage) error
}

// errUnopenable marks an envelope no supplied identity opens.
var errUnopenable = errors.New("sealmigrate: no supplied identity opens the envelope")

// MigrateRuntime re-seals every stored runtime secret to the runtime domain
// recipient alone (ADR 20261022111812, step 3). legacy are the fleet and host
// identities that open envelopes sealed before the domain. The store lists
// only documents with an envelope labelled for another key, so a complete
// migration lists nothing and a rerun scans 0. A document rewritten between
// read and write is left for the next run. Run it only after every vmmd
// reports faas_seal_domain_loaded{domain="runtime"} 1: a re-sealed envelope
// no longer opens with the fleet identity.
func MigrateRuntime(ctx context.Context, store RuntimeStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: runtime migration needs the store, the domain identity and the legacy identities")
	}
	recipient := domain.Recipient()
	kid := recipient.String()
	report := MigrationReport{Domain: sealdomain.Runtime, Target: kid}
	after := ""
	for {
		docs, err := store.ListSecretRekeyDocuments(ctx, kid, after, migrateBatch)
		if err != nil {
			return report, fmt.Errorf("sealmigrate: list runtime secret documents: %w", err)
		}
		for _, doc := range docs {
			if doc.Cursor() <= after {
				return report, errors.New("sealmigrate: runtime secret documents listed out of order")
			}
			after = doc.Cursor()
			report.Scanned++
			if err := resealRuntimeDocument(ctx, store, doc, domain, legacy, recipient, &report); err != nil {
				return report, err
			}
		}
		if len(docs) < migrateBatch {
			return report, nil
		}
	}
}

func resealRuntimeDocument(ctx context.Context, store RuntimeStore, doc state.SecretRekeyDocument, domain *age.X25519Identity, legacy []*age.X25519Identity, recipient *age.X25519Recipient, report *MigrationReport) error {
	kid := recipient.String()
	next, err := state.TransformSecretRekeyDocument(doc, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
		if e.Kid == kid {
			return e, nil
		}
		values, binding, err := secretbox.OpenMultiBound(legacy, e.Ciphertext)
		if err != nil {
			// Sealed to the domain under another label: fix the label only.
			if _, domainErr := secretbox.Open(domain, e.Ciphertext); domainErr == nil {
				e.Kid = kid
				return e, nil
			}
			return e, errUnopenable
		}
		defer clear(values)
		if values[e.Key] == "" {
			return e, errUnopenable
		}
		// Keep the envelope's account binding (ADR-20261023173624).
		sealed, err := secretbox.SealBound([]*age.X25519Recipient{recipient}, binding, values)
		if err != nil {
			return e, fmt.Errorf("sealmigrate: reseal %s %s: %w", doc.Kind, doc.ID, err)
		}
		e.Kid, e.Ciphertext = kid, sealed
		return e, nil
	})
	switch {
	case errors.Is(err, errUnopenable), errors.Is(err, state.ErrConflict):
		// ErrConflict here is a payload the transform cannot parse; like an
		// envelope no identity opens, it is reported, not written.
		report.Failed++
		return nil
	case err != nil:
		return err
	}
	switch err := store.CompareAndSwapSecretRekeyDocument(ctx, doc, next); {
	case errors.Is(err, state.ErrConflict):
		report.Changed++
	case err != nil:
		return fmt.Errorf("sealmigrate: persist %s %s: %w", doc.Kind, doc.ID, err)
	default:
		report.Resealed++
	}
	return nil
}

// RuntimeLister is the slice of *state.PgStore the runtime check reads.
type RuntimeLister interface {
	ListSecretRekeyDocuments(ctx context.Context, kid, after string, limit int) ([]state.SecretRekeyDocument, error)
	CountRuntimeCopiesOffDomain(ctx context.Context, kid string) (int, error)
	ListSealedCrashCaptureKeys(ctx context.Context, afterID string, limit int) ([]state.CrashCaptureKey, error)
}

// CheckRuntime counts, without writing, the rekey documents a migration
// would still rewrite (Pending) and the values vmmd could open only with a
// legacy identity that the migration does not re-seal (Skipped), ADR
// 20261022111812's contract precondition for vmmd:
//   - short-lived copies of runtime secrets filed under another key id: an
//     open config guard's replaced secrets and a cutover's unpublished
//     credentials. They clear as guards close and cutovers finish;
//   - crash capture keys sealed before the runtime domain was staged, which
//     only legacy identities open. They clear as captures expire.
func CheckRuntime(ctx context.Context, store RuntimeLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil {
		return MigrationReport{}, errors.New("sealmigrate: runtime check needs the store and the domain identity")
	}
	kid := domain.Recipient().String()
	report := MigrationReport{Domain: sealdomain.Runtime, Target: kid}
	after := ""
	for {
		docs, err := store.ListSecretRekeyDocuments(ctx, kid, after, migrateBatch)
		if err != nil {
			return report, fmt.Errorf("sealmigrate: list runtime secret documents: %w", err)
		}
		for _, doc := range docs {
			if doc.Cursor() <= after {
				return report, errors.New("sealmigrate: runtime secret documents listed out of order")
			}
			after = doc.Cursor()
			report.Scanned++
			outcome, err := checkRuntimeDocument(doc, domain, legacy, kid)
			if err != nil {
				return report, err
			}
			report.checked(outcome)
		}
		if len(docs) < migrateBatch {
			break
		}
	}
	copies, err := store.CountRuntimeCopiesOffDomain(ctx, kid)
	if err != nil {
		return report, err
	}
	captures, err := runtimeCaptureKeysLeft(ctx, store, domain, legacy)
	if err != nil {
		return report, err
	}
	report.Skipped += copies + captures
	return report, nil
}

// checkRuntimeDocument says what MigrateRuntime would do with doc: rewrite
// it when an envelope opens with a legacy identity, or with the domain under
// another label; report it when an envelope opens with neither.
func checkRuntimeDocument(doc state.SecretRekeyDocument, domain *age.X25519Identity, legacy []*age.X25519Identity, kid string) (resealOutcome, error) {
	outcome := resealCurrent
	_, err := state.TransformSecretRekeyDocument(doc, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
		if e.Kid == kid {
			return e, nil
		}
		if values, _, err := secretbox.OpenMultiBound(legacy, e.Ciphertext); err == nil {
			found := values[e.Key] != ""
			clear(values)
			if !found {
				return e, errUnopenable
			}
			outcome = resealWrite
			return e, nil
		}
		if _, err := secretbox.Open(domain, e.Ciphertext); err == nil {
			outcome = resealWrite
			return e, nil
		}
		return e, errUnopenable
	})
	switch {
	case errors.Is(err, errUnopenable), errors.Is(err, state.ErrConflict):
		return resealFailed, nil
	case err != nil:
		return resealFailed, fmt.Errorf("sealmigrate: check %s %s: %w", doc.Kind, doc.ID, err)
	}
	return outcome, nil
}

// crashCaptureNamespace is pkg/crashcrypt.Namespace; this package does not
// import pkg/crashcrypt, which pulls in the storage backends.
func crashCaptureNamespace(captureID string) string { return "crash_capture:" + captureID }

// runtimeCaptureKeysLeft counts the sealed crash capture keys a legacy
// identity opens and the domain does not.
func runtimeCaptureKeysLeft(ctx context.Context, store RuntimeLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (int, error) {
	if len(legacy) == 0 {
		return 0, nil
	}
	opens := func(ids []*age.X25519Identity, k state.CrashCaptureKey) bool {
		ns, plaintext, err := secretbox.OpenBytesMulti(ids, k.SealedKey)
		clear(plaintext)
		return err == nil && ns == crashCaptureNamespace(k.ID)
	}
	left, after := 0, ""
	for {
		page, err := store.ListSealedCrashCaptureKeys(ctx, after, migrateBatch)
		if err != nil {
			return left, err
		}
		for _, k := range page {
			after = k.ID
			if !opens([]*age.X25519Identity{domain}, k) && opens(legacy, k) {
				left++
			}
		}
		if len(page) < migrateBatch {
			return left, nil
		}
	}
}
