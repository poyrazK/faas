package sealmigrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/basicverifier"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// VerifierReport counts a basic-auth verifier backfill.
type VerifierReport struct {
	Scanned int `json:"scanned"`
	Present int `json:"present"`
	Written int `json:"written"`
	Failed  int `json:"failed"`
}

// BackfillBasicAuthVerifiers writes the argon2id verifier of every sealed
// basic-auth credential that has none (ADR-20261110210000 slice 3h): those
// sealed before verifiers existed, and those an edge re-seal just replaced.
// identities open the edge domain (current, previous and legacy). A blob
// that does not open is counted Failed and left; gateways in verifier mode
// then open it with their sealed fallback, or refuse it in verifier-only
// mode.
func BackfillBasicAuthVerifiers(ctx context.Context, lister EdgeLister, store state.BasicAuthVerifierStore, identities []*age.X25519Identity) (VerifierReport, error) {
	var report VerifierReport
	if lister == nil || store == nil || len(identities) == 0 {
		return report, errors.New("sealmigrate: verifier backfill needs the store and the edge identities")
	}
	afterKind, afterID := "", ""
	for {
		page, err := lister.ListEdgeSealedCredentials(ctx, afterKind, afterID, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, c := range page {
			afterKind, afterID = c.Kind, c.ID
			if c.Kind != state.EdgeSealedCredentialBasicAuth {
				continue
			}
			report.Scanned++
			key := basicverifier.Key(c.Sealed)
			if _, err := store.AppBasicAuthVerifier(ctx, key); err == nil {
				report.Present++
				continue
			} else if !errors.Is(err, state.ErrNotFound) {
				return report, err
			}
			user, pass, ok := openBasicAuth(c.Sealed, identities)
			if !ok {
				report.Failed++
				continue
			}
			verifier, err := basicverifier.New(ctx, user, pass)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: basic auth verifier for %s: %w", c.ID, err)
			}
			if err := store.PutAppBasicAuthVerifier(ctx, key, verifier); err != nil {
				return report, err
			}
			report.Written++
		}
		if len(page) < migrateBatch {
			return report, nil
		}
	}
}

func openBasicAuth(sealed []byte, identities []*age.X25519Identity) (user, pass string, ok bool) {
	ns, plaintext, err := secretbox.OpenBytesMulti(identities, sealed)
	if err != nil || ns != api.AppPublicAuthBasicSealNamespace {
		return "", "", false
	}
	defer clear(plaintext)
	user, pass, found := strings.Cut(string(plaintext), "\n")
	if !found || user == "" || pass == "" {
		return "", "", false
	}
	return user, pass, true
}
