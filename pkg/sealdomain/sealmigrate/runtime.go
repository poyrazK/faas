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

// RuntimeStore is the slice of *state.PgStore the runtime re-seal uses.
// ResealAppSecretForFleet is a generic compare-and-swap ciphertext
// replacement despite its name (ADR-178).
type RuntimeStore interface {
	ListAppSecretsForRekey(ctx context.Context, limit int, cursor string) ([]state.AppSecret, error)
	ResealAppSecretForFleet(ctx context.Context, row state.AppSecret, recipient string, sealed []byte) error
}

// MigrateRuntime re-seals every app secret to the runtime domain recipient
// alone (ADR 20261022111812, step 3). legacy are the fleet and host
// identities that open rows sealed before the domain. A row already sealed
// to the domain is counted current; a row a customer rotated between read
// and write is left for the next run. Run it only after every vmmd reports
// faas_seal_domain_loaded{domain="runtime"} 1: a re-sealed row no longer
// opens with the fleet identity.
func MigrateRuntime(ctx context.Context, store RuntimeStore, domain *age.X25519Identity, legacy []*age.X25519Identity) (MigrationReport, error) {
	if store == nil || domain == nil || len(legacy) == 0 {
		return MigrationReport{}, errors.New("sealmigrate: runtime migration needs the store, the domain identity and the legacy identities")
	}
	recipient := domain.Recipient()
	kid := recipient.String()
	report := MigrationReport{Domain: sealdomain.Runtime, Target: kid}
	cursor := ""
	seen := make(map[string]struct{})
	for {
		rows, err := store.ListAppSecretsForRekey(ctx, migrateBatch, cursor)
		if err != nil {
			return report, fmt.Errorf("sealmigrate: list app secrets: %w", err)
		}
		progressed := false
		for _, row := range rows {
			key := row.AccountID + "\x00" + row.AppID + "\x00" + row.Scope + "\x00" + row.Key
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			progressed = true
			cursor = row.AccountID + "|" + row.AppID + "|" + row.Scope + "|" + row.Key
			report.Scanned++
			if err := resealRuntimeRow(ctx, store, row, domain, legacy, recipient, kid, &report); err != nil {
				return report, err
			}
		}
		if !progressed || len(rows) < migrateBatch {
			return report, nil
		}
	}
}

func resealRuntimeRow(ctx context.Context, store RuntimeStore, row state.AppSecret, domain *age.X25519Identity, legacy []*age.X25519Identity, recipient *age.X25519Recipient, kid string, report *MigrationReport) error {
	env, err := secretbox.OpenMulti(legacy, row.Ciphertext)
	if err != nil {
		if _, domainErr := secretbox.Open(domain, row.Ciphertext); domainErr == nil {
			report.Current++
		} else {
			report.Failed++
		}
		return nil
	}
	sealed, err := secretbox.Seal(recipient, env)
	for k := range env {
		env[k] = ""
		delete(env, k)
	}
	if err != nil {
		return fmt.Errorf("sealmigrate: reseal app=%s scope=%s key=%s: %w", row.AppID, row.Scope, row.Key, err)
	}
	switch err := store.ResealAppSecretForFleet(ctx, row, kid, sealed); {
	case errors.Is(err, state.ErrConflict):
		report.Changed++
	case err != nil:
		return fmt.Errorf("sealmigrate: persist app=%s scope=%s key=%s: %w", row.AppID, row.Scope, row.Key, err)
	default:
		report.Resealed++
	}
	return nil
}
