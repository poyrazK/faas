package liveserver

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/livequery/seal"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

// ResealStore pages every stored live key and login and swaps one seal for
// another; *livekeys.Store is one.
type ResealStore interface {
	SealedKeysAfter(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	ResealKey(ctx context.Context, key livekeys.Sealed, blob []byte) (bool, error)
	SealedLoginsAfter(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	ResealLogin(ctx context.Context, login livekeys.Sealed, blob []byte) (bool, error)
}

// ResealCounts is what a re-seal did with one kind of row.
type ResealCounts struct {
	// Resealed rows held the legacy namespace and now hold their database's.
	Resealed int
	// Bound rows already held their database's namespace.
	Bound int
	// Changed rows were rotated, revoked or re-sealed while the re-seal ran;
	// whatever replaced them was written by a release that seals bound.
	Changed int
	// Refused rows did not open, were bound to another database, named a
	// recipient that is not this livequeryd's, (a key) held another key's
	// secret, or (a login) held a plaintext another row holds too. They are
	// left as they are and must be resolved before release 3 stops opening
	// the legacy form.
	Refused int
}

// ResealReport counts a re-seal's keys and logins.
type ResealReport struct{ Keys, Logins ResealCounts }

// resealPage is how many rows a re-seal reads at a time. Each row is its own
// write, so no transaction spans a page.
const resealPage = 100

// Reseal re-seals every live key and login still sealed under the legacy
// namespace (livekeys.SealNamespace, LoginSealNamespace) to its database's
// bound namespace (ADR-20261016151947, at-rest binding, release 2). apid
// cannot open what it sealed, and rotating a key would issue a secret the
// customer has not deployed, so the re-seal runs where livequeryd's identity
// is: it opens each row with identities and seals the same plaintext to the
// identity's own recipient, never to one a database row names. It is
// idempotent and never logs a plaintext. It refuses, and counts, a row bound
// to another database, a row whose recipient is not one of identities', a
// key holding another key's secret, and a login whose plaintext another row
// holds too: a database writer could have copied it there.
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
	logins := resealKind{name: "login", legacy: livekeys.LoginSealNamespace, bound: livekeys.LoginSealNamespaceFor, maxBytes: livekeys.LoginMaxBytes,
		page: store.SealedLoginsAfter, swap: store.ResealLogin, recipients: recipients}
	shared, err := logins.sharedPlaintexts(ctx, identities)
	if err != nil {
		return report, err
	}
	logins.shared = shared
	err = logins.run(ctx, identities, log, &report.Logins)
	return report, err
}

// resealKind is one table's re-seal: its namespaces, plaintext cap and
// store calls, and for keys a check of the plaintext against the row.
type resealKind struct {
	name     string
	legacy   string
	bound    func(databaseID string) string
	maxBytes int
	page     func(ctx context.Context, after string, limit int) ([]livekeys.Sealed, error)
	swap     func(ctx context.Context, row livekeys.Sealed, blob []byte) (bool, error)
	check    func(row livekeys.Sealed, plain []byte) error
	// recipients are the loaded identities' recipients by their string
	// form: the only ones a row is re-sealed to.
	recipients map[string]*age.X25519Recipient
	// shared holds the digests of plaintexts that more than one row holds
	// (logins, whose plaintext names no row).
	shared map[[sha256.Size]byte]bool
}

// sharedPlaintexts reads every row once and returns the digests of the
// plaintexts more than one row holds. A legacy blob names no database, so a
// copy of another database's login onto this row is told apart only by its
// plaintext appearing twice; the re-seal must not bind either copy.
func (k resealKind) sharedPlaintexts(ctx context.Context, identities []*age.X25519Identity) (map[[sha256.Size]byte]bool, error) {
	seen := map[[sha256.Size]byte]int{}
	for after := ""; ; {
		rows, err := k.page(ctx, after, resealPage)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if _, plain, err := secretbox.OpenBytesMulti(identities, row.Blob); err == nil {
				seen[sha256.Sum256(plain)]++
				clear(plain)
			}
		}
		if len(rows) < resealPage {
			break
		}
		after = rows[len(rows)-1].ID
	}
	shared := map[[sha256.Size]byte]bool{}
	for digest, n := range seen {
		if n > 1 {
			shared[digest] = true
		}
	}
	return shared, nil
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
	if k.check != nil {
		if err := k.check(row, plain); err != nil {
			refuse(err.Error())
			return nil
		}
	}
	if k.shared[sha256.Sum256(plain)] {
		refuse("another row holds the same " + k.name)
		return nil
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
