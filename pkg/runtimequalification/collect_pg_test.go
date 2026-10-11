package runtimequalification

// adr: 687

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// The native owner is synthetic; the publication/receipt store is real Postgres.
func TestCollectPostgresReceiptRequiresCompletedImport(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	f := newEvidenceFixtureWithStore(t, s)
	c, fixture := collectionFixture(t, f)
	before, err := s.DeploymentByID(t.Context(), fixture.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	owner := &fakeNativeSession{fixture: f}
	bundle, err := Collect(t.Context(), s, f.artifacts, c, fixture, f.trust, f.key, func(context.Context, NativeConfig, Fixture) (NativeSession, error) { return owner, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RuntimeReleaseQualification(t.Context(), f.release.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("collection alone created eligibility", err)
	}
	if _, err := Import(t.Context(), s, f.artifacts, Inputs{bytes.NewReader(bundle.Envelope), bytes.NewReader(bundle.TestMetal), bytes.NewReader(bundle.Leakcheck)}, f.trust); err != nil {
		t.Fatal(err)
	}
	if err := state.RequireRuntimeReleaseQualification(t.Context(), s, f.release); err != nil {
		t.Fatal("completed import did not grant preparation eligibility", err)
	}
	after, err := s.DeploymentByID(t.Context(), fixture.DeploymentID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("collection/import changed customer deployment", err)
	}
}
