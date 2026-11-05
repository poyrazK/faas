package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// logDrainNamespace is the namespace apid seals log drain authentication
// headers under; apid and gatewayd-internal each define it.
const logDrainNamespace = "APP_LOG_DRAIN_AUTH_HEADER"

// EdgeStore is the slice of *state.PgStore the edge re-seal uses.
type EdgeStore interface {
	ListEdgeSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.EdgeSealedCredential, error)
	ResealEdgeCredential(ctx context.Context, c state.EdgeSealedCredential, sealed []byte) (bool, error)
}

// MigrateEdge re-seals app basic-auth credentials and log drain headers to
// the edge domain recipient alone (ADR 20261022111812, step 3). legacy are
// the fleet and host identities that open credentials sealed before the
// domain. Rows an application standard manages are counted, not written:
// its log destinations are immutable versions and its drains must carry the
// destination's exact ciphertext. Run it only after every gatewayd-internal
// holds the domain identity: a re-sealed credential no longer opens with the
// fleet identity.
func MigrateEdge(ctx context.Context, store EdgeStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: edge migration needs the store, the domain identity and the legacy identities")
	}
	report := MigrationReport{Domain: sealdomain.Edge, Target: domain.Recipient().String()}
	afterKind, afterID := "", ""
	for {
		page, err := store.ListEdgeSealedCredentials(ctx, afterKind, afterID, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, c := range page {
			report.Scanned++
			afterKind, afterID = c.Kind, c.ID
			if c.Managed {
				if _, _, err := secretbox.OpenBytesMulti([]*age.X25519Identity{domain}, c.Sealed); err == nil {
					report.Current++
				} else {
					report.Skipped++
				}
				continue
			}
			namespace, maxBytes := logDrainNamespace, api.AppLogDrainAuthHeaderMaxBytes
			if c.Kind == state.EdgeSealedCredentialBasicAuth {
				namespace, maxBytes = api.AppPublicAuthBasicSealNamespace, api.AppPublicAuthBasicMaxBytes
			}
			sealed, outcome, err := resealBlob(c.Sealed, namespace, maxBytes, domain, legacy)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: reseal edge credential %s %s: %w", c.Kind, c.ID, err)
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = store.ResealEdgeCredential(ctx, c, sealed); err != nil {
					return report, err
				}
			}
			report.count(outcome, replaced)
		}
		if len(page) < migrateBatch {
			return report, nil
		}
	}
}
