package liveserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// ResealStore pages every stored live key and login, swaps a key's seal for
// another and drops a legacy login; *livekeys.Store is one.
type ResealStore interface {
	SealedKeysAfter(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	ResealKey(ctx context.Context, key livekeys.Sealed, blob []byte) (bool, error)
	SealedLoginsAfter(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	DropLegacyLogin(ctx context.Context, login livekeys.Sealed) (bool, error)
}

// ResealCounts is what a re-seal did with one kind of row.
type ResealCounts struct {
	// Resealed keys held the legacy namespace and now hold their database's.
	Resealed int
	// Dropped logins held the legacy namespace and were removed. apid issues
	// each such database a new login, sealed bound to it.
	Dropped int
	// Bound rows already held their database's namespace.
	Bound int
	// Changed rows were rotated, revoked or re-sealed while the re-seal ran;
	// whatever replaced them was written by a release that seals bound.
	Changed int
	// Refused rows did not open, were bound to another database, or (a key)
	// named a recipient that is not this livequeryd's or held another key's
	// secret. They are left as they are and must be resolved before release
	// 3 stops opening the legacy form.
	Refused int
}

// ResealReport counts a re-seal's keys and logins.
type ResealReport struct{ Keys, Logins ResealCounts }

// resealPage is how many rows a re-seal reads at a time. Each row is its own
// write, so no transaction spans a page.
const resealPage = 100

// Reseal moves every live key and login off the legacy namespace
// (livekeys.SealNamespace, LoginSealNamespace) for its database's bound one
// (ADR-20261016151947, at-rest binding, release 2). It runs where
// livequeryd's identity is, opening each row with identities, is idempotent
// and never logs a plaintext.
//
// A key is re-sealed: rotating it would issue a secret the customer has not
// deployed. The same plaintext is sealed to the identity's own recipient,
// never to one a database row names, and a key holding another key's
// secret is refused. A login is dropped instead (amended 2026-10-28): a
// legacy blob names no database, so the re-seal cannot tell a database's own
// login from one a database writer moved onto its row, and binding it would
// make the move permanent. apid issues each database that has a live key
// and no login a new one, sealed bound to it. Rows bound to another
// database, and rows that do not open, are refused and counted.
func Reseal(ctx context.Context, store ResealStore, identities []*age.X25519Identity, log *slog.Logger) (ResealReport, error) {
	var report ResealReport
	recipients := make(map[string]*age.X25519Recipient, len(identities))
	for _, id := range identities {
		recipients[id.Recipient().String()] = id.Recipient()
	}
	keys := resealKind{name: "key", legacy: livekeys.SealNamespace, bound: livekeys.KeySealNamespaceFor, maxBytes: livekeys.KeyMaxBytes,
		page: store.SealedKeysAfter, swap: store.ResealKey, check: keyHoldsItsSecret, recipients: recipients}
	if err := keys.run(ctx, identities, log, &report.Keys); err != nil {
		return report, err
	}
	logins := resealKind{name: "login", legacy: livekeys.LoginSealNamespace, bound: livekeys.LoginSealNamespaceFor,
		page: store.SealedLoginsAfter, drop: store.DropLegacyLogin}
	err := logins.run(ctx, identities, log, &report.Logins)
	return report, err
}

// resealKind is one table's re-seal: its namespaces and store calls. Keys
// set swap, maxBytes, check and recipients and are re-sealed; logins set
// drop and are removed.
type resealKind struct {
	name     string
	legacy   string
	bound    func(databaseID string) string
	maxBytes int
	page     func(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	swap     func(ctx context.Context, row livekeys.Sealed, blob []byte) (bool, error)
	drop     func(ctx context.Context, row livekeys.Sealed) (bool, error)
	check    func(row livekeys.Sealed, plain []byte) error
	// recipients are the loaded identities' recipients by their string
	// form: the only ones a key is re-sealed to.
	recipients map[string]*age.X25519Recipient
}

func (k resealKind) run(ctx context.Context, identities []*age.X25519Identity, log *slog.Logger, counts *ResealCounts) error {
	for after := ""; ; {
		rows, err := k.page(ctx, after, resealPage)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := k.one(ctx, row, identities, log, counts); err != nil {
				return err
			}
		}
		if len(rows) < resealPage {
			return nil
		}
		after = rows[len(rows)-1].ID
	}
}

// one re-seals row. A row it must leave alone is counted and logged by ID,
// never by content; only a store or sealing failure is an error.
func (k resealKind) one(ctx context.Context, row livekeys.Sealed, identities []*age.X25519Identity, log *slog.Logger, counts *ResealCounts) error {
	refuse := func(reason string) {
		counts.Refused++
		log.WarnContext(ctx, "livequeryd: live "+k.name+" left as it is", "id", row.ID, "database", row.DatabaseID, "reason", reason)
	}
	namespace, plain, err := secretbox.OpenBytesMulti(identities, row.Blob)
	if err != nil {
		refuse("it does not open with this identity")
		return nil
	}
	defer clear(plain)
	switch {
	case row.DatabaseID != "" && namespace == k.bound(row.DatabaseID):
		counts.Bound++
		return nil
	case namespace != k.legacy:
		refuse(fmt.Sprintf("it is sealed for %q", namespace))
		return nil
	}
	if k.drop != nil {
		dropped, err := k.drop(ctx, row)
		switch {
		case err != nil:
			return err
		case dropped:
			counts.Dropped++
			log.InfoContext(ctx, "livequeryd: legacy live "+k.name+" dropped; apid issues a new one", "database", row.DatabaseID)
		default:
			counts.Changed++
		}
		return nil
	}
	if k.check != nil {
		if err := k.check(row, plain); err != nil {
			refuse(err.Error())
			return nil
		}
	}
	// The recipient column is data a database writer controls: re-sealing to
	// it would hand them the plaintext.
	recipient, ok := k.recipients[row.Recipient]
	if !ok {
		refuse("its recipient is not this livequeryd's identity")
		return nil
	}
	blob, err := secretbox.SealBytes(recipient, k.bound(row.DatabaseID), plain, k.maxBytes)
	if err != nil {
		return fmt.Errorf("livequeryd: re-seal live %s %s: %w", k.name, row.ID, err)
	}
	swapped, err := k.swap(ctx, row, blob)
	if err != nil {
		return err
	}
	if swapped {
		counts.Resealed++
	} else {
		counts.Changed++
	}
	return nil
}

// keyHoldsItsSecret is openKey's check: a key's plaintext is a secret whose
// key ID is the row's, so another key's secret copied onto this row is not
// re-sealed (and so laundered) under this database's namespace.
func keyHoldsItsSecret(row livekeys.Sealed, plain []byte) error {
	secret, err := seal.ParseSecret(string(plain))
	if err != nil {
		return errors.New("its secret does not parse")
	}
	if secret.KID != row.ID {
		return fmt.Errorf("it holds the secret of key %s", secret.KID)
	}
	return nil
}
