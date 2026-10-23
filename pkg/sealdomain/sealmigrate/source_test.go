package sealmigrate_test

import (
	"bytes"
	"context"
	"sort"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/state"
)

// webhookSecrets is an in-memory github_webhook_secrets table.
type webhookSecrets struct {
	rows map[int64]state.GithubWebhookSecret
	// rotate makes the next write to this installation lose once.
	rotate int64
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
