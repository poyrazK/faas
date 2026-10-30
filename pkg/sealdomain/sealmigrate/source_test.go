package sealmigrate_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// webhookSecrets is an in-memory github_webhook_secrets table.
type webhookSecrets struct {
	rows map[int64]state.GithubWebhookSecret
	// rotate makes the next write to this installation lose once.
	rotate int64
	// tokens are the unexpired install tokens, in (account, installation)
	// order.
	tokens []state.GithubInstallTokenSealed
}

func (f *webhookSecrets) ListUnexpiredGithubInstallTokens(_ context.Context, afterAccountID string, afterInstallationID int64, limit int) ([]state.GithubInstallTokenSealed, error) {
	var out []state.GithubInstallTokenSealed
	for _, t := range f.tokens {
		after := t.AccountID > afterAccountID || (t.AccountID == afterAccountID && t.InstallationID > afterInstallationID)
		if after && len(out) < limit {
			out = append(out, t)
		}
	}
	return out, nil
}

func (f *webhookSecrets) ListGithubWebhookSecrets(_ context.Context, afterID int64, limit int) ([]state.GithubWebhookSecretRow, error) {
	var out []state.GithubWebhookSecretRow
	for id, s := range f.rows {
		if id > afterID {
			out = append(out, state.GithubWebhookSecretRow{InstallationID: id, Secret: s})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstallationID < out[j].InstallationID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *webhookSecrets) lose(id int64) bool {
	if id == f.rotate {
		f.rotate = 0
		return true
	}
	return false
}

func (f *webhookSecrets) SealGithubWebhookSecret(_ context.Context, id int64, plain, sealed []byte) (bool, error) {
	s := f.rows[id]
	if f.lose(id) || !bytes.Equal(s.Value, plain) || len(s.Sealed) > 0 {
		return false, nil
	}
	s.Sealed = sealed
	f.rows[id] = s
	return true, nil
}

func (f *webhookSecrets) ResealGithubWebhookSecret(_ context.Context, id int64, previous, sealed []byte) (bool, error) {
	s := f.rows[id]
	if f.lose(id) || !bytes.Equal(s.Sealed, previous) {
		return false, nil
	}
	s.Sealed = sealed
	f.rows[id] = s
	return true, nil
}

func TestMigrateSource(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	copyFor := func(secret string, to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		blob, err := sealdomain.SealGithubWebhookSecret(rs, []byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	store := &webhookSecrets{rotate: 3, rows: map[int64]state.GithubWebhookSecret{
		1: {Value: []byte("fleet-copy"), Sealed: copyFor("fleet-copy", fleet)},
		2: {Value: []byte("dual-copy"), Sealed: copyFor("dual-copy", domain, fleet)},
		3: {Value: []byte("rotated"), Sealed: copyFor("rotated", fleet)},
		4: {Value: []byte("no-copy")},
		5: {Value: []byte("done"), Sealed: copyFor("done", domain)},
		6: {Value: []byte("stranger"), Sealed: copyFor("stranger", stranger)},
	}}
	report, err := sealmigrate.MigrateSource(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Source, Target: domain.Recipient().String(), Scanned: 6, Resealed: 3, Current: 1, Changed: 1, Failed: 1}
	if report != want {
		t.Fatalf("report = %+v\nwant     %+v", report, want)
	}
	for _, id := range []int64{1, 2, 4, 5} {
		s := store.rows[id]
		if opened, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{domain}, s.Sealed); err != nil || !bytes.Equal(opened, s.Value) {
			t.Fatalf("installation %d: domain open = %q, %v", id, opened, err)
		}
		if _, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, s.Sealed); err == nil {
			t.Fatalf("installation %d still opens with the fleet identity", id)
		}
	}
	again, err := sealmigrate.MigrateSource(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	// The rotated row moves on the second run; the stranger's copy stays failed.
	if want := (sealmigrate.MigrationReport{Domain: sealdomain.Source, Target: domain.Recipient().String(), Scanned: 6, Resealed: 1, Current: 4, Failed: 1}); again != want {
		t.Fatalf("second run = %+v\nwant          %+v", again, want)
	}
}

func TestMigrateSource_RequiresIdentities(t *testing.T) {
	domain := mustIdentity(t)
	if _, err := sealmigrate.MigrateSource(context.Background(), &webhookSecrets{}, domain, nil); err == nil {
		t.Fatal("migrating without legacy identities succeeded")
	}
	if _, err := sealmigrate.MigrateSource(context.Background(), &webhookSecrets{}, nil, []*age.X25519Identity{domain}); err == nil {
		t.Fatal("migrating without the domain identity succeeded")
	}
}

// TestCheckSource counts the webhook secrets a migration would still seal
// and the unexpired install tokens only a legacy identity opens, writing
// nothing: the precondition for githubd dropping the fleet and host
// identities. The migration reports the same tokens as skipped: they are
// not re-sealed, and expire within the hour.
func TestCheckSource(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	copyFor := func(secret string, to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		blob, err := sealdomain.SealGithubWebhookSecret(rs, []byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	token := func(to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		blob, err := secretbox.SealTo(rs, secretbox.Envelope{"GITHUB_INSTALL_TOKEN": "ghs_check"})
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	store := &webhookSecrets{rows: map[int64]state.GithubWebhookSecret{
		1: {Value: []byte("fleet-copy"), Sealed: copyFor("fleet-copy", fleet)},
		2: {Value: []byte("domain-copy"), Sealed: copyFor("domain-copy", domain)},
		3: {Value: []byte("no-copy")},
	}, tokens: []state.GithubInstallTokenSealed{
		{AccountID: "a1", InstallationID: 1, Sealed: token(fleet)},
		{AccountID: "a2", InstallationID: 2, Sealed: token(domain, fleet)},
		{AccountID: "a3", InstallationID: 3, Sealed: token(domain)},
	}}
	before := map[int64][]byte{}
	for id, s := range store.rows {
		before[id] = s.Sealed
	}
	ctx := context.Background()
	report, err := sealmigrate.CheckSource(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Source, Target: domain.Recipient().String(), Scanned: 3, Current: 1, Pending: 2, Skipped: 1}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}
	for id, s := range store.rows {
		if !bytes.Equal(s.Sealed, before[id]) {
			t.Fatalf("the check rewrote installation %d", id)
		}
	}

	migrated, err := sealmigrate.MigrateSource(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || migrated.Resealed != 2 || migrated.Skipped != 1 {
		t.Fatalf("migration = %+v, %v; want two copies sealed and the fleet-only token skipped", migrated, err)
	}
	if report, err := sealmigrate.CheckSource(ctx, store, domain, []*age.X25519Identity{fleet}); err != nil || report.Pending != 0 || report.Skipped != 1 {
		t.Fatalf("check after the re-seal = %+v, %v; want only the token left", report, err)
	}
	store.tokens = store.tokens[1:] // the fleet-only token expired
	if report, err := sealmigrate.CheckSource(ctx, store, domain, []*age.X25519Identity{fleet}); err != nil || report.Pending != 0 || report.Skipped != 0 {
		t.Fatalf("check once the token expired = %+v, %v; want nothing left", report, err)
	}
	if report, err := sealmigrate.CheckSource(ctx, store, domain, nil); err != nil || report.Skipped != 0 {
		t.Fatalf("check without legacy identities = %+v, %v; want no token left", report, err)
	}
}
