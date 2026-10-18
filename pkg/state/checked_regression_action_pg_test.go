//go:build !no_pg

// adr: 20261017210000
package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgCheckedRegressionActionAtomicReceipt(t *testing.T) {
	ctx := t.Context()
	store := NewPgStore(pgtest.OpenMigrated(t))
	account, err := store.CreateAccount(ctx, "checked-triage-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "checked-triage", Type: AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	var factor, zero pgtype.Numeric
	if err := factor.Scan("2.00"); err != nil {
		t.Fatal(err)
	}
	if err := zero.Scan("0"); err != nil {
		t.Fatal(err)
	}
	appID := pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}
	depID := pgtype.UUID{Bytes: uuid.New(), Valid: true}
	if err := store.UpsertRegressionObservation(ctx, sqlc.UpsertRegressionObservationParams{AppID: appID, DeploymentID: depID, Route: "GET /orders", P95Ms: 200, P95BaseMs: 100, AffectedCount: 10, RegressionFactor: factor, Kind: "latency", ErrorRatePct: zero, ErrorRateBasePct: zero}); err != nil {
		t.Fatal(err)
	}
	selection := sqlc.GetRegressionObservationParams{AppID: appID, DeploymentID: depID, Route: "GET /orders"}
	read := func() sqlc.DebugRegressionObservation {
		t.Helper()
		row, err := store.GetRegressionObservation(ctx, selection)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	before := read()
	arg := sqlc.ApplyRegressionActionParams{AppID: appID, DeploymentID: depID, Route: selection.Route, State: "resolved", ExpectedVersion: before.ReviewVersion}
	digest := sha256.Sum256([]byte("exact reviewed request"))
	key := "checked-regression:" + hex.EncodeToString(digest[:])
	failure := errors.New("injected render failure")
	_, _, err = store.ApplyCheckedRegressionAction(ctx, account.ID, digest, "Investigated", arg, func(sqlc.DebugRegressionObservation) ([]byte, error) { return nil, failure })
	if !errors.Is(err, failure) || read().ReviewVersion != before.ReviewVersion {
		t.Fatal("render failure committed observation", err)
	}
	if _, _, err := store.GetIdempotent(ctx, account.ID, key); !errors.Is(err, ErrNotFound) {
		t.Fatal("rollback retained an orphan reservation", err)
	}
	expected := []byte(`{"receipt":"committed"}`)
	body, replayed, err := store.ApplyCheckedRegressionAction(ctx, account.ID, digest, "Investigated", arg, func(sqlc.DebugRegressionObservation) ([]byte, error) { return expected, nil })
	if err != nil || replayed || !bytes.Equal(body, expected) {
		t.Fatal("fresh atomic action failed", replayed, err)
	}
	committed := read()
	if committed.State != "resolved" || committed.ReviewVersion == before.ReviewVersion {
		t.Fatal("action was not committed")
	}
	body, replayed, err = store.ApplyCheckedRegressionAction(ctx, account.ID, digest, "Investigated", arg, func(sqlc.DebugRegressionObservation) ([]byte, error) {
		t.Fatal("replay rendered or updated again")
		return nil, nil
	})
	if err != nil || !replayed || !bytes.Equal(body, expected) || read().ReviewVersion != committed.ReviewVersion {
		t.Fatal("replay failed to retain original result", err)
	}
	// Invalid output cannot leave a successful mutation without a usable receipt.
	arg.ExpectedVersion = committed.ReviewVersion
	arg.State = "active"
	_, _, err = store.ApplyCheckedRegressionAction(ctx, account.ID, sha256.Sum256([]byte("invalid output")), "Investigated", arg, func(sqlc.DebugRegressionObservation) ([]byte, error) { return []byte("invalid-json"), nil })
	if err == nil || read().ReviewVersion != committed.ReviewVersion {
		t.Fatal("invalid response committed action")
	}
	arg.ExpectedVersion.Valid = false
	_, _, err = store.ApplyCheckedRegressionAction(ctx, account.ID, sha256.Sum256([]byte("missing guard")), "Investigated", arg, func(sqlc.DebugRegressionObservation) ([]byte, error) { return expected, nil })
	if !errors.Is(err, ErrInvalidArgument) || read().ReviewVersion != committed.ReviewVersion {
		t.Fatal("unguarded native action accepted", err)
	}
}
