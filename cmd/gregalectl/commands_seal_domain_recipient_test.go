package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"os"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSealDomainVerifyRecipientPostgres pins the check the seal_domains role
// runs before apid seals to a domain: the staged recipient must be the one
// readers published the probe with (ADR 20261022111812).
func TestSealDomainVerifyRecipientPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	// pgtest isolates a test in a clone database or in a schema on the
	// search_path; carry both into the command's DSN.
	u.Path = "/" + pool.Config().ConnConfig.Database
	if sp := pool.Config().ConnConfig.RuntimeParams["search_path"]; sp != "" {
		q := u.Query()
		q.Set("search_path", sp)
		u.RawQuery = q.Encode()
	}
	dsn := u.String()
	var out bytes.Buffer
	previous := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = previous })

	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	reader, stranger := newID(), newID()
	dir := t.TempDir()
	stage := func(id *age.X25519Identity) {
		path := sealdomain.IdentityPath(dir, sealdomain.Build) + ".pub"
		// The recipient file is written read-only.
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := secretbox.WriteRecipientFile(path, id); err != nil {
			t.Fatal(err)
		}
	}
	verify := func() (sealDomainVerifyReport, error) {
		out.Reset()
		err := verifySealDomainRecipient(sealdomain.Build, dir, dsn, "", true)
		var report sealDomainVerifyReport
		if err == nil {
			if jerr := json.Unmarshal(out.Bytes(), &report); jerr != nil {
				t.Fatalf("decode %q: %v", out.String(), jerr)
			}
		}
		return report, err
	}

	stage(reader)
	if report, err := verify(); err != nil || report.ProbePublished || report.Ready {
		t.Fatalf("before any probe = %+v, %v; want a pass with nothing to compare", report, err)
	}
	blob, err := sealdomain.SealProbe(sealdomain.Build, reader.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	if err := state.NewPgStore(pool).UpsertSealDomainProbe(ctx, string(sealdomain.Build), reader.Recipient().String(), blob); err != nil {
		t.Fatal(err)
	}
	if report, err := verify(); err != nil || !report.ProbePublished || !report.Ready || report.Recipient != reader.Recipient().String() {
		t.Fatalf("matching recipient = %+v, %v; want ready", report, err)
	}
	stage(stranger)
	if _, err := verify(); err == nil {
		t.Fatal("a recipient from another pair passed")
	}
}
