//go:build !no_pg

package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPgBuildExportPublicationLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	buildExportLifecycle(t, s)
}
func TestPgBuildExportPublicationSubstitution(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	buildExportClaimSubstitution(t, s)
}

func TestPgBuildExportPublicationNonwaitingAndImmutable(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	in, app, dep, b, _ := buildExportFixture(t, s)
	for _, target := range []string{"app", "deployment", "build", "publisher", "artifact"} {
		t.Run(target, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			switch target {
			case "app":
				_, err = tx.Exec(t.Context(), `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, app.ID)
			case "deployment":
				_, err = tx.Exec(t.Context(), `SELECT id FROM deployments WHERE id=$1 FOR UPDATE`, dep.ID)
			case "build":
				_, err = tx.Exec(t.Context(), `SELECT id FROM builds WHERE id=$1 FOR UPDATE`, b.ID)
			case "publisher":
				_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::text,0))`, app.ID)
			case "artifact":
				_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.'||$1::text,0))`, dep.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			bounded, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.RecordBuildExportPublication(bounded, in); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatal("publication waited or bypassed fence", err)
			}
		})
	}
	if _, err := s.RecordBuildExportPublication(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE build_export_publications SET expires_at=expires_at+interval '1 minute' WHERE id=$1`, in.ID); err == nil {
		t.Fatal("raw expiry mutation accepted")
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM build_export_publications WHERE id=$1`, in.ID); err == nil {
		t.Fatal("live owner evidence erased")
	}
}

func TestPgBuildExportPublicationKeyRotation(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	buildExportKeyRotation(t, s)
}

func TestPgBuildExportPublicationOwnerErasure(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	buildID := buildExportOwnerErasure(t, s)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM build_export_publications WHERE build_id=$1`, buildID).Scan(&count); err != nil || count != 0 {
		t.Fatal("erased export bytes retained", count, err)
	}
}
