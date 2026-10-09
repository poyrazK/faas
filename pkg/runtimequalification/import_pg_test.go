package runtimequalification

// adr: 740

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Synthetic evidence tests the real production store adapter, not native KVM.
func TestImportPostgresImmutableAndRevocable(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	f := newEvidenceFixtureWithStore(t, s)
	beforeDep, err := s.DeploymentByID(t.Context(), f.report.Native.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	beforeApp, err := s.AppByID(t.Context(), beforeDep.AppID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust)
	if err != nil || !again.RecordedAt.Equal(first.RecordedAt) || first.ReportSHA256 != SHA256(f.raw) {
		t.Fatal("Postgres import replaced receipt", again, err)
	}
	afterDep, err := s.DeploymentByID(t.Context(), beforeDep.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterApp, err := s.AppByID(t.Context(), beforeApp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeDep, afterDep) || !reflect.DeepEqual(beforeApp, afterApp) {
		t.Fatal("operator import changed customer state")
	}
	if err := state.RequireRuntimeReleaseQualification(t.Context(), s, f.release); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeRuntimeReleaseQualification(t.Context(), f.release.ID, first.ReportSHA256, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(t.Context(), s, f.artifacts, f.inputs(), f.trust); !errors.Is(err, state.ErrConflict) {
		t.Fatal("Postgres import revived revocation", err)
	}
	if err := state.RequireRuntimeReleaseQualification(t.Context(), s, f.release); !errors.Is(err, state.ErrConflict) {
		t.Fatal("Postgres quarantine lost", err)
	}
}
