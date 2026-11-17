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
	EdgeLister
	ResealEdgeCredential(ctx context.Context, c state.EdgeSealedCredential, sealed []byte) (bool, error)
}

// EdgeLister is the slice of *state.PgStore the edge check reads.
type EdgeLister interface {
	ListEdgeSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.EdgeSealedCredential, error)
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
	return walkEdge(ctx, store, domain, legacy, store.ResealEdgeCredential)
}

// CheckEdge counts, without writing, the edge credentials that legacy
// identities still open (ADR 20261022111812, the contract step's
// precondition): gatewayd-internal may drop the fleet and host identities
// only once Pending and Skipped are zero. A managed row the domain does not
// open is Skipped: only a new version of its application standard's log
// destination replaces it.
func CheckEdge(ctx context.Context, store EdgeLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil {
		return MigrationReport{}, errors.New("sealmigrate: edge check needs the store and the domain identity")
	}
	return walkEdge(ctx, store, domain, legacy, nil)
}

// walkEdge visits every listed edge credential. With reseal nil it only
// counts.
func walkEdge(ctx context.Context, store EdgeLister, domain *age.X25519Identity, legacy []*age.X25519Identity, reseal func(ctx context.Context, c state.EdgeSealedCredential, sealed []byte) (bool, error)) (MigrationReport, error) {
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
			switch c.Kind {
			case state.EdgeSealedCredentialBasicAuth:
				namespace, maxBytes = api.AppPublicAuthBasicSealNamespace, api.AppPublicAuthBasicMaxBytes
			case state.EdgeSealedCredentialHeaderSecret:
				namespace, maxBytes = api.EdgeRuleHeaderSecretSealNamespace, api.EdgeRuleHeaderSecretMaxBytes
			}
			sealed, outcome, err := resealBlob(c.Sealed, namespace, maxBytes, domain, legacy)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: reseal edge credential %s %s: %w", c.Kind, c.ID, err)
			}
			if reseal == nil {
				report.checked(outcome)
				continue
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = reseal(ctx, c, sealed); err != nil {
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
