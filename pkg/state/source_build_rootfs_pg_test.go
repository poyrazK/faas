//go:build !no_pg

package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestPgSourceBuildRootfsLifecycle(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceBuildRootfsLifecycle(t, s)
}

func TestPgSourceBuildRootfsQueuedClaimsAreFenced(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	f := sourceBuildRootfsFixture(t, s)
	queuedID := uuid.NewString()
	// An older queued row can still be claimed. It must not change the
	// latest-started build between the approval check and rootfs stamp.
	if _, err := pool.Exec(t.Context(), `INSERT INTO builds(id,deployment_id,kind,source_bytes,status) VALUES($1,$2,'tarball',1,'queued')`, queuedID, f.Dep.ID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	input, _, err := prepareSourceBuildRootfs(f.Input)
	if err != nil {
		t.Fatal(err)
	}
	if err := lockSourceBuildRootfsParents(t.Context(), tx, input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := s.ClaimQueuedBuild(ctx, queuedID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("queued claim bypassed publication fence", err)
	}
	if queued, err := s.BuildByID(t.Context(), queuedID); err != nil || queued.Status != BuildQueued {
		t.Fatal("queued claim changed while the publication lock was held", err)
	}
	if err := tx.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	// An autocommit query cancelled by its caller may finish after unlock.
	// Either that claim or this retry must leave a running latest build.
	if _, err := s.ClaimQueuedBuild(t.Context(), queuedID); err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if claimed, err := s.BuildByID(t.Context(), queuedID); err != nil || claimed.Status != BuildRunning {
		t.Fatal("unlocked queued claim did not run", err)
	}
	if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); !errors.Is(err, ErrApplicationStandardRuntimeStale) {
		t.Fatal("newly claimed build failed to invalidate old approval", err)
	}
}
func TestPgSourceBuildRootfsSuperseded(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	sourceBuildRootfsSuperseded(t, s)
}
func TestPgSourceBuildRootfsLateIntent(t *testing.T) {
	for _, mode := range []string{"command", "manifest", "require_signed", "security_policy"} {
		t.Run(mode, func(t *testing.T) { s, _ := registryVerificationPGStore(t); sourceBuildRootfsLateIntent(t, s, mode) })
	}
}

func TestPgSourceBuildRootfsSubstitution(t *testing.T) {
	for _, mode := range []string{"approval", "owner", "scope", "base hash", "injected guest", "missing runner", "wrong kind", "new build", "queued build"} {
		t.Run(mode, func(t *testing.T) { s, _ := registryVerificationPGStore(t); sourceBuildRootfsSubstitution(t, s, mode) })
	}
}

func TestPgSourceBuildRootfsRollsBackFailedStamp(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	f := sourceBuildRootfsFixture(t, s)
	// Force a storage write failure after the producer INSERT. Both it and
	// the current pointer must roll back with deployment metadata.
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION refuse_source_fixture_stamp() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture stamp failure';END;$$;
	CREATE TRIGGER refuse_source_fixture_stamp BEFORE UPDATE OF rootfs_path ON deployments FOR EACH ROW EXECUTE FUNCTION refuse_source_fixture_stamp();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishSourceBuildRootfs(t.Context(), f.Input); err == nil {
		t.Fatal("failed stamp published")
	}
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM source_build_rootfs WHERE id=$1`, f.Input.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed stamp left producer", count, err)
	}
	dep, err := s.DeploymentByID(t.Context(), f.Dep.ID)
	if err != nil || dep.RootfsPath != f.Dep.RootfsPath || dep.RootfsKey != f.Dep.RootfsKey || dep.RootfsBytes != f.Dep.RootfsBytes {
		t.Fatal("failed transaction partially stamped", err)
	}
}

func TestPgSourceBuildRootfsOwnerErasure(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	id := sourceBuildRootfsOwnerErasure(t, s)
	var count int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM source_build_rootfs WHERE id=$1`, id).Scan(&count); err != nil || count != 0 {
		t.Fatal("erasure retained producer", count, err)
	}
}

func TestPgSourceBuildRootfsNonwaitingAndImmutable(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	f := sourceBuildRootfsFixture(t, s)
	for _, target := range []string{"app", "deployment", "build", "approval", "publisher", "artifact", "base"} {
		t.Run(target, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			switch target {
			case "app":
				_, err = tx.Exec(t.Context(), `SELECT id FROM apps WHERE id=$1 FOR UPDATE`, f.App.ID)
			case "deployment":
				_, err = tx.Exec(t.Context(), `SELECT id FROM deployments WHERE id=$1 FOR UPDATE`, f.Dep.ID)
			case "build":
				_, err = tx.Exec(t.Context(), `SELECT id FROM builds WHERE id=$1 FOR UPDATE`, f.Build.ID)
			case "approval":
				_, err = tx.Exec(t.Context(), `SELECT id FROM build_export_publications WHERE id=$1 FOR UPDATE`, f.Parent.ID)
			case "publisher":
				_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.controls.'||$1::text,0))`, f.App.ID)
			case "artifact":
				_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock_shared(hashtextextended('gregale.application-standard.artifact-children.'||$1::text,0))`, f.Dep.ID)
			case "base":
				_, err = tx.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended('gregale.base-producer.'||$1::text,0))`, f.Base.Input.Artifact.StorageKey)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := s.PublishSourceBuildRootfs(ctx, f.Input); !errors.Is(err, ErrApplicationStandardRuntimeBusy) {
				t.Fatal("publication waited or bypassed fence", err)
			}
		})
	}
	value, err := s.PublishSourceBuildRootfs(t.Context(), f.Input)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`UPDATE source_build_rootfs SET expires_at=expires_at+interval '1 minute' WHERE id=$1`, `DELETE FROM source_build_rootfs WHERE id=$1`, `UPDATE source_build_rootfs_current SET artifact_id=artifact_id WHERE artifact_id=$1`, `DELETE FROM source_build_rootfs_current WHERE artifact_id=$1`} {
		if _, err := pool.Exec(t.Context(), query, value.ID); err == nil {
			t.Fatal("raw evidence mutation accepted")
		}
	}
	if _, err := pool.Exec(t.Context(), `DELETE FROM builds WHERE id=$1`, f.Build.ID); err == nil {
		t.Fatal("live build deletion erased source producer history")
	}
}

func TestPgSourceBuildRootfsSoftDeleteRestore(t *testing.T) {
	s, pool := registryVerificationPGStore(t)
	id := sourceBuildRootfsSoftDeleteRestore(t, s)
	if _, err := pool.Exec(t.Context(), `DELETE FROM source_build_rootfs WHERE id=$1`, id); err == nil {
		t.Fatal("restored live owner evidence erased")
	}
}
