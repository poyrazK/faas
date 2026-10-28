// Package sealmigrate re-seals stored envelopes into their seal domain (ADR
// 20261022111812, migration step 3). It runs from gregalectl on a
// control-plane node, never inside a daemon.
package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// Namespaces the migration re-seals under. They match the writers': the
// outbound one is pkg/outbound.ManagedAuthorizationSealNamespace, which this
// package does not import to stay below pkg/outbound.
const (
	outboundNamespace = "outbound_authorization"
	outboundMaxBytes  = 8192
	registryNamespace = "registry_creds"
	migrateBatch      = 200
)

// resealOutcome is what resealBlob decided for one envelope.
type resealOutcome int

const (
	// resealWrite: the envelope was re-sealed to the domain; write it.
	resealWrite resealOutcome = iota
	// resealCurrent: only the domain opens the envelope already.
	resealCurrent
	// resealFailed: no identity opens it, or it is under another namespace.
	resealFailed
)

// resealBlob opens a SealBytes envelope with the legacy identities and
// re-seals it under namespace to the domain alone.
func resealBlob(blob []byte, namespace string, maxBytes int, domain *age.X25519Identity, legacy []*age.X25519Identity) ([]byte, resealOutcome, error) {
	ns, plaintext, err := secretbox.OpenBytesMulti(legacy, blob)
	if err != nil {
		if _, _, domainErr := secretbox.OpenBytesMulti([]*age.X25519Identity{domain}, blob); domainErr == nil {
			return nil, resealCurrent, nil
		}
		return nil, resealFailed, nil
	}
	defer clear(plaintext)
	if ns != namespace {
		return nil, resealFailed, nil
	}
	sealed, err := secretbox.SealBytesTo([]*age.X25519Recipient{domain.Recipient()}, namespace, plaintext, maxBytes)
	if err != nil {
		return nil, resealFailed, err
	}
	return sealed, resealWrite, nil
}

// count records one envelope's outcome; replaced says whether a write landed.
func (r *MigrationReport) count(outcome resealOutcome, replaced bool) {
	switch {
	case outcome == resealCurrent:
		r.Current++
	case outcome == resealFailed:
		r.Failed++
	case replaced:
		r.Resealed++
	default:
		r.Changed++
	}
}

// MigrationReport counts one migrate run. Resealed rows changed; Current
// rows were already sealed as the target asks; Changed rows were rotated
// by a customer between read and write and are left for the next run;
// Failed rows opened with no supplied identity. Skipped rows cannot be
// re-sealed in place (the edge domain's application-standard log
// destinations and the drains they manage, and the account domain's
// project environment clone values); they keep opening with the fleet
// identity.
type MigrationReport struct {
	Domain   sealdomain.Domain `json:"domain"`
	Target   string            `json:"target"`
	Scanned  int               `json:"scanned"`
	Resealed int               `json:"resealed"`
	Current  int               `json:"current"`
	Changed  int               `json:"changed"`
	Failed   int               `json:"failed"`
	Skipped  int               `json:"skipped,omitempty"`
}

// OutboundStore is the slice of *state.PgStore the outbound re-seal uses.
type OutboundStore interface {
	ListOutboundSealedCredentials(ctx context.Context, afterKind, afterID string, limit int) ([]state.OutboundSealedCredential, error)
	ResealOutboundCredential(ctx context.Context, c state.OutboundSealedCredential, sealed []byte) (bool, error)
}

// MigrateOutbound re-seals every outbound credential to the outbound domain
// recipient alone (ADR 20261022111812, migration step 3). legacy are the
// fleet and host identities that open rows sealed before the domain; a row
// that opens with legacy is re-sealed, a row that opens only with domain is
// already current. Run it only after every outboundd holds the domain
// identity: a re-sealed row no longer opens with the fleet identity.
func MigrateOutbound(ctx context.Context, store OutboundStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: outbound migration needs the store, the domain identity and the legacy identities")
	}
	report := MigrationReport{Domain: sealdomain.Outbound, Target: domain.Recipient().String()}
	afterKind, afterID := "", ""
	for {
		page, err := store.ListOutboundSealedCredentials(ctx, afterKind, afterID, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, c := range page {
			report.Scanned++
			afterKind, afterID = c.Kind, c.ID
			sealed, outcome, err := resealBlob(c.Sealed, outboundNamespace, outboundMaxBytes, domain, legacy)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: reseal outbound %s %s: %w", c.Kind, c.ID, err)
			}
			replaced := false
			if outcome == resealWrite {
				if replaced, err = store.ResealOutboundCredential(ctx, c, sealed); err != nil {
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

// GithubWebhookStore is the slice of *state.PgStore the webhook-secret
// backfill uses.
type GithubWebhookStore interface {
	ListUnsealedGithubWebhookSecrets(ctx context.Context, afterID int64, limit int) ([]state.GithubWebhookSecretRow, error)
	SealGithubWebhookSecret(ctx context.Context, installationID int64, plain, sealed []byte) (bool, error)
}

// SealGithubWebhookSecrets writes the sealed copy of every webhook secret
// that has none yet, sealed to recipients (the source domain's write
// recipients). The plaintext column stays until a later release drops it.
func SealGithubWebhookSecrets(ctx context.Context, store GithubWebhookStore, recipients []*age.X25519Recipient) (MigrationReport, error) {
	if store == nil || len(recipients) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: webhook secret backfill needs the store and a recipient")
	}
	report := MigrationReport{Domain: sealdomain.Source, Target: recipients[0].String()}
	var after int64
	for {
		page, err := store.ListUnsealedGithubWebhookSecrets(ctx, after, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, row := range page {
			report.Scanned++
			after = row.InstallationID
			sealed, err := sealdomain.SealGithubWebhookSecret(recipients, row.Secret.Value)
			if err != nil {
				return report, fmt.Errorf("sealmigrate: seal webhook secret %d: %w", row.InstallationID, err)
			}
			updated, err := store.SealGithubWebhookSecret(ctx, row.InstallationID, row.Secret.Value, sealed)
			if err != nil {
				return report, err
			}
			if updated {
				report.Resealed++
			} else {
				report.Changed++
			}
		}
		if len(page) < migrateBatch {
			return report, nil
		}
	}
}
