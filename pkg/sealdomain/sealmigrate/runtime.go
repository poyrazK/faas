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
