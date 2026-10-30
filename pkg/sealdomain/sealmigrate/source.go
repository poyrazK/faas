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

// SourceStore is the slice of *state.PgStore the source re-seal uses.
type SourceStore interface {
	SourceLister
	sourceWriter
}

// SourceLister is the slice of *state.PgStore the source check reads.
type SourceLister interface {
	ListGithubWebhookSecrets(ctx context.Context, afterID int64, limit int) ([]state.GithubWebhookSecretRow, error)
	ListUnexpiredGithubInstallTokens(ctx context.Context, afterAccountID string, afterInstallationID int64, limit int) ([]state.GithubInstallTokenSealed, error)
}

type sourceWriter interface {
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
// githubd seals its successor under the domain's write mode. The unexpired
// ones only a legacy identity opens are reported as skipped. Run this only
// after every githubd holds the domain identity: a re-sealed copy no longer
// opens with the fleet identity.
func MigrateSource(ctx context.Context, store SourceStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: source migration needs the store, the domain identity and the legacy identities")
	}
	return walkSource(ctx, store, domain, legacy, store)
}

// CheckSource counts, without writing, the webhook secrets a migration
// would still seal (Pending) and the unexpired install tokens only a legacy
// identity opens (Skipped) (ADR 20261022111812, the contract step's
// precondition): githubd may drop the fleet and host identities only once
// both are zero.
func CheckSource(ctx context.Context, store SourceLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil {
		return MigrationReport{}, errors.New("sealmigrate: source check needs the store and the domain identity")
	}
	return walkSource(ctx, store, domain, legacy, nil)
}

// walkSource visits every webhook secret, then counts the install tokens
// left on legacy identities. With w nil it only counts.
func walkSource(ctx context.Context, store SourceLister, domain *age.X25519Identity, legacy []*age.X25519Identity, w sourceWriter) (MigrationReport, error) {
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
			if err := resealSourceRow(ctx, w, row, domain, legacy, recipients, &report); err != nil {
				return report, err
			}
		}
		if len(page) < migrateBatch {
			break
		}
	}
	left, err := sourceTokensLeft(ctx, store, domain, legacy)
	if err != nil {
		return report, err
	}
	report.Skipped += left
	return report, nil
}

// sourceTokensLeft counts the unexpired install tokens a legacy identity
// opens and the domain does not. githubd rehydrates such a token instead of
// minting a new one, so a githubd without the legacy identities fails on it
// until it expires, within the hour.
func sourceTokensLeft(ctx context.Context, store SourceLister, domain *age.X25519Identity, legacy []*age.X25519Identity) (int, error) {
	if len(legacy) == 0 {
		return 0, nil
	}
	opens := func(ids []*age.X25519Identity, blob []byte) bool {
		_, err := secretbox.OpenMulti(ids, blob)
		return err == nil
	}
	left := 0
	afterAccount, afterInstallation := "", int64(0)
	for {
		page, err := store.ListUnexpiredGithubInstallTokens(ctx, afterAccount, afterInstallation, migrateBatch)
		if err != nil {
			return left, err
		}
		for _, t := range page {
			afterAccount, afterInstallation = t.AccountID, t.InstallationID
			if !opens([]*age.X25519Identity{domain}, t.Sealed) && opens(legacy, t.Sealed) {
				left++
			}
		}
		if len(page) < migrateBatch {
			return left, nil
		}
	}
}

// resealSourceRow seals or re-seals one webhook secret's copy. With w nil it
// only counts: a row the migration would write is pending.
func resealSourceRow(ctx context.Context, w sourceWriter, row state.GithubWebhookSecretRow, domain *age.X25519Identity, legacy []*age.X25519Identity, recipients []*age.X25519Recipient, report *MigrationReport) error {
	var replaced bool
	if len(row.Secret.Sealed) == 0 {
		if len(row.Secret.Value) == 0 {
			report.Failed++
			return nil
		}
		if w == nil {
			report.Pending++
			return nil
		}
		sealed, err := sealdomain.SealGithubWebhookSecret(recipients, row.Secret.Value)
		if err != nil {
			return fmt.Errorf("sealmigrate: seal webhook secret %d: %w", row.InstallationID, err)
		}
		if replaced, err = w.SealGithubWebhookSecret(ctx, row.InstallationID, row.Secret.Value, sealed); err != nil {
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
		if w == nil {
			clear(secret)
			report.Pending++
			return nil
		}
		sealed, err := sealdomain.SealGithubWebhookSecret(recipients, secret)
		clear(secret)
		if err != nil {
			return fmt.Errorf("sealmigrate: reseal webhook secret %d: %w", row.InstallationID, err)
		}
		if replaced, err = w.ResealGithubWebhookSecret(ctx, row.InstallationID, row.Secret.Sealed, sealed); err != nil {
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
