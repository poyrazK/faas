package rekey

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Durable envelopes are the work journal: restart scans outstanding documents
// from the beginning, including copies inserted behind the previous cursor.
// A failure advances only this run's pagination, so it cannot starve later rows
// or permanently skip a failed document. CAS rejects concurrent replacements.
func (r *Replayer) runSecretDocuments(ctx context.Context, store state.SecretRekeyStore, progress func(RekeyProgress)) error {
	if progress == nil {
		progress = func(RekeyProgress) {}
	}
	var p RekeyProgress
	after := ""
	delay := time.Second * time.Duration(r.cfg.BatchSize) / time.Duration(r.cfg.RowsPerSecond)
	interval := time.Second / time.Duration(r.cfg.RowsPerSecond)
	var previousEnvelope time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := store.ListSecretRekeyDocuments(ctx, r.currentKid, after, r.cfg.BatchSize)
		if err != nil {
			return fmt.Errorf("rekey: list sealed documents: %w", err)
		}
		if len(batch) == 0 {
			progress(p)
			return nil
		}
		for _, doc := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if doc.Cursor() <= after {
				return fmt.Errorf("rekey: unordered sealed document page")
			}
			after, p.LastID = doc.Cursor(), doc.Cursor()
			p.Total++
			next, err := state.TransformSecretRekeyDocument(doc, func(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
				if err := ctx.Err(); err != nil {
					return e, err
				}
				// A saved capture can contain many envelopes. Charge the existing
				// row-rate budget for each encryption, rather than each document.
				if e.Kid != r.currentKid {
					if remaining := interval - time.Since(previousEnvelope); remaining > 0 {
						select {
						case <-ctx.Done():
							return e, ctx.Err()
						case <-time.After(remaining):
						}
					}
					previousEnvelope = time.Now()
				}
				return r.resealEnvelope(e)
			})
			if err == nil {
				err = store.CompareAndSwapSecretRekeyDocument(ctx, doc, next)
			}
			if err != nil {
				p.Failed++
			} else {
				p.Rekeyed++
			}
		}
		progress(p)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (r *Replayer) resealEnvelope(e state.SecretRekeyEnvelope) (state.SecretRekeyEnvelope, error) {
	if e.Kid == r.currentKid {
		return e, nil
	}
	values, err := secretbox.OpenMulti(r.identities, e.Ciphertext)
	if err != nil {
		return e, fmt.Errorf("rekey: cannot open sealed envelope")
	}
	if values[e.Key] == "" {
		return e, fmt.Errorf("rekey: sealed envelope has no matching secret")
	}
	sealed, err := secretbox.Seal(r.active, values)
	if err != nil {
		return e, fmt.Errorf("rekey: cannot reseal envelope")
	}
	e.Kid, e.Ciphertext = r.currentKid, sealed
	return e, nil
}
