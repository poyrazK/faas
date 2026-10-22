package neon

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestBoundedCredentialEnforcesConnectionLimitBeforeDelivery(t *testing.T) {
	f := newCredentialFixture(t)
	role := f.readonly
	role.connectionLimit = 2
	ctx := t.Context()
	if err := f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal(err)
	}
	first := f.connect(t, role.name)
	_ = f.connect(t, role.name)
	cfg := f.config.Copy()
	cfg.User = role.name
	cfg.Password = "local-fixture-password"
	third, err := pgx.ConnectConfig(ctx, cfg)
	if err == nil {
		_ = third.Close(ctx)
		t.Fatal("credential exceeded its reserved connection ceiling")
	}
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "53300" {
		t.Fatalf("wrong connection refusal: %v", err)
	}
	if err = first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replacement.Close(ctx) }()
	if err = f.manager.Ensure(ctx, f.material, role); err != nil {
		t.Fatal("bounded credential retry failed", err)
	}
	role.connectionLimit = 3
	if err = f.manager.Ensure(ctx, f.material, role); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatal("changed ceiling reused an existing credential", err)
	}
}
