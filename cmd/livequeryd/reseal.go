package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

var (
	reseal        = flag.Bool("reseal", false, "re-seal every live key and login still under the legacy namespace to its database's (ADR-20261016151947 at-rest binding, release 2) with -keys-identity, then exit; idempotent")
	resealDSNFile = flag.String("reseal-dsn-file", "", "with -reseal: file holding a control-plane connection string whose role may update managed_postgres_live_keys and managed_postgres_live_logins")
)

// runReseal is `livequeryd -reseal`, an operator-run backfill. It runs on a
// hub host because only livequeryd's identity opens what apid sealed. It
// fails when any row was left as it is, so release 3 (which stops opening the
// legacy form) waits until every row is resolved.
func runReseal(ctx context.Context, log *slog.Logger) error {
	if *keysIdentity == "" || *resealDSNFile == "" {
		return errors.New("livequeryd: -reseal needs -keys-identity and -reseal-dsn-file")
	}
	identity, err := secretbox.LoadHostKey(*keysIdentity)
	if err != nil {
		return fmt.Errorf("livequeryd: live key identity: %w", err)
	}
	data, err := os.ReadFile(*resealDSNFile) //nolint:forbidigo // operator-provisioned file named by a flag
	if err != nil {
		return fmt.Errorf("livequeryd: read -reseal-dsn-file: %w", err)
	}
	cfg, err := pgxpool.ParseConfig(strings.TrimSpace(string(data)))
	if err != nil {
		return fmt.Errorf("livequeryd: control-plane connection string: %w", err)
	}
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = "faas-livequeryd-reseal"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fmt.Errorf("livequeryd: control-plane pool: %w", err)
	}
	defer pool.Close()
	report, err := liveserver.Reseal(ctx, livekeys.New(pool), []*age.X25519Identity{identity}, log)
	log.InfoContext(ctx, "livequeryd: re-seal finished",
		"keys_resealed", report.Keys.Resealed, "keys_bound", report.Keys.Bound, "keys_changed", report.Keys.Changed, "keys_refused", report.Keys.Refused,
		"logins_resealed", report.Logins.Resealed, "logins_bound", report.Logins.Bound, "logins_changed", report.Logins.Changed, "logins_refused", report.Logins.Refused)
	if err != nil {
		return err
	}
	if refused := report.Keys.Refused + report.Logins.Refused; refused > 0 {
		return fmt.Errorf("livequeryd: %d live keys or logins were left as they are; see the warnings above", refused)
	}
	return nil
}
