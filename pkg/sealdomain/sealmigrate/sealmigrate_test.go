package sealmigrate_test

import (
	"bytes"
	"context"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

type outboundRows struct {
	rows []state.OutboundSealedCredential
	// rotate makes the compare-and-swap of this ID lose, as if the customer
	// rotated the key between the read and the write.
	rotate string
}

func (f *outboundRows) ListOutboundSealedCredentials(_ context.Context, afterKind, afterID string, limit int) ([]state.OutboundSealedCredential, error) {
	var out []state.OutboundSealedCredential
	for _, r := range f.rows {
		if r.Kind > afterKind || (r.Kind == afterKind && r.ID > afterID) {
			out = append(out, r)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *outboundRows) ResealOutboundCredential(_ context.Context, c state.OutboundSealedCredential, sealed []byte) (bool, error) {
	if c.ID == f.rotate {
		return false, nil
	}
	for i := range f.rows {
		if f.rows[i].Kind == c.Kind && f.rows[i].ID == c.ID && bytes.Equal(f.rows[i].Sealed, c.Sealed) {
			f.rows[i].Sealed = sealed
			return true, nil
		}
	}
	return false, nil
}

func TestMigrateOutbound(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	seal := func(ns string, to ...*age.X25519Identity) []byte {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		blob, err := secretbox.SealBytesTo(rs, ns, []byte("Bearer sk-"+ns), 0)
		if err != nil {
			t.Fatal(err)
		}
		return blob
	}
	store := &outboundRows{rotate: "c-rotated", rows: []state.OutboundSealedCredential{
		{Kind: "pool", ID: "a-dual", Sealed: seal("outbound_authorization", domain, fleet)},
		{Kind: "pool", ID: "b-done", Sealed: seal("outbound_authorization", domain)},
		{Kind: "pool", ID: "c-rotated", Sealed: seal("outbound_authorization", fleet)},
		{Kind: "pool", ID: "d-stranger", Sealed: seal("outbound_authorization", stranger)},
		{Kind: "pool", ID: "e-wrong-namespace", Sealed: seal("app_webhook", fleet)},
		{Kind: "primary", ID: "f-fleet", Sealed: seal("outbound_authorization", fleet)},
	}}
	report, err := sealmigrate.MigrateOutbound(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Outbound, Target: domain.Recipient().String(), Scanned: 6, Resealed: 2, Current: 1, Changed: 1, Failed: 2}
	if report != want {
		t.Fatalf("report = %+v\nwant     %+v", report, want)
	}
	for _, r := range store.rows {
		_, _, fleetErr := secretbox.OpenBytes(fleet, r.Sealed)
		switch r.ID {
		case "a-dual", "f-fleet", "b-done":
			if fleetErr == nil {
				t.Errorf("%s still opens with the fleet identity after the re-seal", r.ID)
			}
			if _, plain, err := secretbox.OpenBytes(domain, r.Sealed); err != nil || string(plain) != "Bearer sk-outbound_authorization" {
				t.Errorf("%s: domain open = %q, %v", r.ID, plain, err)
			}
		case "c-rotated":
			if fleetErr != nil {
				t.Errorf("%s: a lost compare-and-swap rewrote the row", r.ID)
			}
		}
	}

	again, err := sealmigrate.MigrateOutbound(context.Background(), store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if again.Resealed != 0 || again.Current != 3 {
		t.Fatalf("second run = %+v; want nothing re-sealed and three rows current (idempotent)", again)
	}
}

func TestMigrateOutbound_RequiresIdentities(t *testing.T) {
	id := mustIdentity(t)
	if _, err := sealmigrate.MigrateOutbound(context.Background(), &outboundRows{}, id, nil); err == nil {
		t.Fatal("migrated without legacy identities")
	}
	if _, err := sealmigrate.MigrateOutbound(context.Background(), &outboundRows{}, nil, []*age.X25519Identity{id}); err == nil {
		t.Fatal("migrated without the domain identity")
	}
}

type webhookRows struct {
	rows map[int64]state.GithubWebhookSecret
}

func (f *webhookRows) ListUnsealedGithubWebhookSecrets(_ context.Context, afterID int64, limit int) ([]state.GithubWebhookSecretRow, error) {
	var out []state.GithubWebhookSecretRow
	for id := afterID + 1; id <= 10 && len(out) < limit; id++ {
		if s, ok := f.rows[id]; ok && len(s.Sealed) == 0 {
			out = append(out, state.GithubWebhookSecretRow{InstallationID: id, Secret: s})
		}
	}
	return out, nil
}

func (f *webhookRows) SealGithubWebhookSecret(_ context.Context, id int64, plain, sealed []byte) (bool, error) {
	s := f.rows[id]
	if !bytes.Equal(s.Value, plain) || len(s.Sealed) != 0 {
		return false, nil
	}
	s.Sealed = sealed
	f.rows[id] = s
	return true, nil
}

func TestSealGithubWebhookSecrets(t *testing.T) {
	fleet := mustIdentity(t)
	store := &webhookRows{rows: map[int64]state.GithubWebhookSecret{
		1: {Value: []byte("0123456789abcdef")},
		2: {Value: []byte("fedcba9876543210"), Sealed: []byte("already")},
		3: {Value: []byte("aaaaaaaaaaaaaaaa")},
	}}
	report, err := sealmigrate.SealGithubWebhookSecrets(context.Background(), store, []*age.X25519Recipient{fleet.Recipient()})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 2 || report.Resealed != 2 {
		t.Fatalf("report = %+v; want the two unsealed rows sealed", report)
	}
	for _, id := range []int64{1, 3} {
		got, err := sealdomain.OpenGithubWebhookSecret([]*age.X25519Identity{fleet}, store.rows[id].Sealed)
		if err != nil || !bytes.Equal(got, store.rows[id].Value) {
			t.Fatalf("row %d: sealed copy opens to %q, %v", id, got, err)
		}
	}
	if string(store.rows[2].Sealed) != "already" {
		t.Fatal("a row that already had a sealed copy was rewritten")
	}
}

func mustIdentity(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
