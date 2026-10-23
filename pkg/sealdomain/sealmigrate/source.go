package sealmigrate

import (
	"context"
	"errors"
	"fmt"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/state"
)

// SourceStore is the slice of *state.PgStore the source re-seal uses.
type SourceStore interface {
	ListGithubWebhookSecrets(ctx context.Context, afterID int64, limit int) ([]state.GithubWebhookSecretRow, error)
	SealGithubWebhookSecret(ctx context.Context, installationID int64, plain, sealed []byte) (bool, error)
	ResealGithubWebhookSecret(ctx context.Context, installationID int64, previous, sealed []byte) (bool, error)
}

// MigrateSource seals every GitHub webhook secret's copy to the source
// domain recipient alone (ADR 20261022111812, step 3):
//   - a copy a legacy (fleet or host) identity opens is re-sealed from what
//     it opens to;
//   - a row without a copy gets one, from the secret;
//   - a copy only the domain opens is current;
//   - a copy no identity opens is reported as failed and left alone, because
//     githubd refuses it rather than fall back to the plaintext column.
//
// Install tokens are not re-sealed: each expires within the hour, and
// githubd seals its successor under the domain's write mode. Run this only
// after every githubd holds the domain identity: a re-sealed copy no longer
// opens with the fleet identity.
func MigrateSource(ctx context.Context, store SourceStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: source migration needs the store, the domain identity and the legacy identities")
	}
	recipients := []*age.X25519Recipient{domain.Recipient()}
	report := MigrationReport{Domain: sealdomain.Source, Target: recipients[0].String()}
	var after int64
	for {
		page, err := store.ListGithubWebhookSecrets(ctx, after, migrateBatch)
		if err != nil {
			return report, err
		}
		for _, row := range page {
			report.Scanned++
			after = row.InstallationID
			if err := resealSourceRow(ctx, store, row, domain, legacy, recipients, &report); err != nil {
				return report, err
			}
		}
		if len(page) < migrateBatch {
			return report, nil
		}
	}
}

func resealSourceRow(ctx context.Context, store SourceStore, row state.GithubWebhookSecretRow, domain *age.X25519Identity, legacy []*age.X25519Identity, recipients []*age.X25519Recipient, report *MigrationReport) error {
	var replaced bool
	if len(row.Secret.Sealed) == 0 {
		if len(row.Secret.Value) == 0 {
			report.Failed++
			return nil
		}
		sealed, err := sealdomain.SealGithubWebhookSecret(recipients, row.Secret.Value)
		if err != nil {
			return fmt.Errorf("sealmigrate: seal webhook secret %d: %w", row.InstallationID, err)
		}
		if replaced, err = store.SealGithubWebhookSecret(ctx, row.InstallationID, row.Secret.Value, sealed); err != nil {
			return err
		}
	} else {
		secret, err := sealdomain.OpenGithubWebhookSecret(legacy, row.Secret.Sealed)
		if err != nil {
			if _, domainErr := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{domain}, row.Secret.Sealed); domainErr == nil {
				report.Current++
			} else {
				report.Failed++
			}
			return nil
		}
		sealed, err := sealdomain.SealGithubWebhookSecret(recipients, secret)
		clear(secret)
		if err != nil {
			return fmt.Errorf("sealmigrate: reseal webhook secret %d: %w", row.InstallationID, err)
		}
		if replaced, err = store.ResealGithubWebhookSecret(ctx, row.InstallationID, row.Secret.Sealed, sealed); err != nil {
			return err
		}
	}
	if replaced {
		report.Resealed++
	} else {
		report.Changed++
	}
	return nil
}
