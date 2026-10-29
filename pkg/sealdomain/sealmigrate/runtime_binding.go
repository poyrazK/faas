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

// RuntimeBindingStore is the slice of *state.PgStore the binding pass uses:
// every release-secret rekey document with the account that owns it.
type RuntimeBindingStore interface {
	ListSecretBindingDocuments(ctx context.Context, after string, limit int) ([]state.SecretBindingDocument, error)
	CompareAndSwapSecretRekeyDocument(ctx context.Context, before state.SecretRekeyDocument, after json.RawMessage) error
}

// maxReportedMismatches caps the document cursors a BindingReport lists.
const maxReportedMismatches = 100

// BindingReport counts one binding pass. Resealed documents had an envelope
// re-sealed; Current documents were bound and labelled for the domain
// already. Mismatched documents hold an envelope bound to another account:
// the pass never rebinds one, because that is the copy the binding exists to
// catch. Failed documents have an envelope no supplied identity opens, a
// payload the transform cannot parse, or no owning account.
type BindingReport struct {
	MigrationReport
	Mismatched          int      `json:"mismatched"`
	MismatchedDocuments []string `json:"mismatched_documents,omitempty"`
}

// errForeignBinding marks an envelope bound to an account other than the
// document's owner.
var errForeignBinding = errors.New("sealmigrate: envelope is bound to another account")

// BindRuntime re-seals every stored runtime secret bound to the account of
// the document that holds it (ADR-20261023173624, binding pass). It opens
// envelopes with the runtime domain identity and the legacy identities, and
// re-seals each unbound one, or one sealed for another key, to the domain
// recipient alone; the runtime migration's precondition therefore applies.
// A complete pass leaves every envelope bound, so a rerun re-seals nothing.
// A document rewritten between read and write is left for the next run.
func BindRuntime(ctx context.Context, store RuntimeBindingStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (BindingReport, error) {
	if store == nil || domain == nil {
		return BindingReport{}, errors.New("sealmigrate: the binding pass needs the store and the domain identity")
	}
	recipient := domain.Recipient()
	identities := append([]*age.X25519Identity{domain}, legacy...)
	report := BindingReport{MigrationReport: MigrationReport{Domain: sealdomain.Runtime, Target: recipient.String()}}
	after := ""
	for {
		docs, err := store.ListSecretBindingDocuments(ctx, after, migrateBatch)
		if err != nil {
			return report, fmt.Errorf("sealmigrate: list runtime secret documents: %w", err)
		}
		for _, doc := range docs {
			if doc.Cursor() <= after {
				return report, errors.New("sealmigrate: runtime secret documents listed out of order")
			}
			after = doc.Cursor()
			report.Scanned++
			if err := bindRuntimeDocument(ctx, store, doc, identities, recipient, &report); err != nil {
				return report, err
			}
		}
		if len(docs) < migrateBatch {
			return report, nil
		}
	}
}

func bindRuntimeDocument(ctx context.Context, store RuntimeBindingStore, doc state.SecretBindingDocument, identities []*age.X25519Identity, recipient *age.X25519Recipient, report *BindingReport) error {
	if doc.AccountID == "" {
		report.Failed++
		return nil
	}
	kid := recipient.String()
	binding := secretbox.Binding{AccountID: doc.AccountID}
	resealed := false
	next, err := state.TransformSecretRekeyDocument(doc.SecretRekeyDocument, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
		values, current, err := secretbox.OpenMultiBound(identities, e.Ciphertext)
		if err != nil {
			return e, errUnopenable
		}
		defer clear(values)
		if values[e.Key] == "" {
			return e, errUnopenable
		}
		if !current.IsZero() {
			if current.Check(doc.AccountID) != nil {
				return e, errForeignBinding
			}
			if e.Kid == kid {
				return e, nil
			}
		}
		sealed, err := secretbox.SealBound([]*age.X25519Recipient{recipient}, binding, values)
		if err != nil {
			return e, fmt.Errorf("sealmigrate: bind %s %s: %w", doc.Kind, doc.ID, err)
		}
		e.Kid, e.Ciphertext = kid, sealed
		resealed = true
		return e, nil
	})
	switch {
	case errors.Is(err, errForeignBinding):
		report.Mismatched++
		if len(report.MismatchedDocuments) < maxReportedMismatches {
			report.MismatchedDocuments = append(report.MismatchedDocuments, doc.Cursor())
		}
		return nil
	case errors.Is(err, errUnopenable), errors.Is(err, state.ErrConflict):
		report.Failed++
		return nil
	case err != nil:
		return err
	case !resealed:
		report.Current++
		return nil
	}
	switch err := store.CompareAndSwapSecretRekeyDocument(ctx, doc.SecretRekeyDocument, next); {
	case errors.Is(err, state.ErrConflict):
		report.Changed++
	case err != nil:
		return fmt.Errorf("sealmigrate: persist %s %s: %w", doc.Kind, doc.ID, err)
	default:
		report.Resealed++
	}
	return nil
}
