//go:build !no_pg

package state

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestReleaseSecretRekeyPG(t *testing.T) {
	testReleaseSecretRekey(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}

func TestSecretKeyRetirementSerializesWritersPG(t *testing.T) {
	for _, first := range []string{"writer", "retirement", "repeatable_retirement"} {
		t.Run(first, func(t *testing.T) {
			ctx := t.Context()
			pool := pgtest.OpenMigrated(t)
			store := NewPgStore(pool)
			account, err := store.CreateAccount(ctx, "retirement-race@example.test", api.PlanPro)
			mustReleaseSecret(t, err)
			project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "retirement-race"})
			mustReleaseSecret(t, err)
			identity, _ := age.GenerateX25519Identity()
			kid := identity.Recipient().String()
			values, _ := json.Marshal([]map[string]any{{"key": "TOKEN", "kid": kid, "ciphertext": []byte("sealed-fixture")}})
			params := sqlc.InsertProjectReleaseSecretBundleParams{AccountID: mustPgUUID(account.ID), ProjectID: mustPgUUID(project.ID), RequestID: strings.Repeat("e", 32), RequestHash: strings.Repeat("f", 64), ValuesJson: values, ResponseJson: []byte(`{}`)}
			q := sqlc.New()
			tx, err := pool.Begin(ctx)
			mustReleaseSecret(t, err)
			defer func() { _ = tx.Rollback(ctx) }()
			result := make(chan error, 1)
			if first == "writer" {
				_, err := q.InsertProjectReleaseSecretBundle(ctx, tx, params)
				mustReleaseSecret(t, err)
				go func() { result <- store.RetireSecretEncryptionKey(ctx, kid) }()
			} else {
				mustReleaseSecret(t, q.LockSecretEncryptionKeyRetirement(ctx, tx))
				var writer sqlc.DBTX = pool
				if first == "repeatable_retirement" {
					older, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
					mustReleaseSecret(t, err)
					defer func() { _ = older.Rollback(ctx) }()
					_, err = q.ReadSecretKeyRetirementFence(ctx, older)
					mustReleaseSecret(t, err)
					writer = older
				}
				mustReleaseSecret(t, q.AdvanceSecretKeyRetirementFence(ctx, tx))
				mustReleaseSecret(t, q.RetireSecretEncryptionKey(ctx, tx, kid))
				go func() { _, err := q.InsertProjectReleaseSecretBundle(ctx, writer, params); result <- mapErr(err) }()
			}
			select {
			case err := <-result:
				t.Fatalf("operation crossed an uncommitted retirement fence: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			mustReleaseSecret(t, tx.Commit(ctx))
			select {
			case err := <-result:
				var pgError *pgconn.PgError
				serialization := errors.As(err, &pgError) && pgError.Code == "40001"
				if !errors.Is(err, ErrConflict) && !serialization {
					t.Fatalf("referenced or retired key admitted: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("retirement fence did not unblock")
			}
			usage, err := store.SecretEncryptionKeyUsage(ctx, kid)
			mustReleaseSecret(t, err)
			if first == "writer" && usage.StagedBundles != 1 {
				t.Fatal("retirement lost the committed writer")
			}
			if first != "writer" && !usage.Empty() {
				t.Fatal("late writer persisted an envelope under the retired key")
			}
		})
	}
}
