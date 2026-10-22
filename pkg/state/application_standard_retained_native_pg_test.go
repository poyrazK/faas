//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgApplicationStandardRetainedNativeAdmission(t *testing.T) {
	s, _ := state.RuntimeCapturePGStore(t)
	retainedNativeAdmissionLifecycle(t, s)
}

func TestPgApplicationStandardUninstalledNativeScope(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	uninstalledNativeScopeFixture(t, s, func(app state.App, project state.Project) error {
		_, err := pool.Exec(t.Context(), `UPDATE apps SET project_id=$1 WHERE id=$2`, project.ID, app.ID)
		return err
	})
}

func TestPgApplicationStandardRetainedNativeRawProtocol(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	app, dep, _ := retainedNativeFixture(t, s)
	scan, err := s.GetCurrentDeploymentRuntimeScan(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	scan = state.PublishNativeComposedScan(t, s, app, dep, &scan.Input.Reports[0].Report)
	ins, capture := state.CreateRuntimeArtifactCapture(t, s, app, dep)
	b := state.RuntimeCaptureTestBinding(t, s, ins)
	b.Incarnation = uuid.NewString()
	state.RegisterConsumedNativeIdentity(t, s, b, runtimeadmission.ArtifactProtocolVersion)
	b.ExpiresAtUnixNano = scan.ExpiresAt.UnixNano()
	insert := func() error {
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		_, err = pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, b.Token, ins.ID, ins.State, raw)
		return err
	}
	if err := insert(); !errors.Is(state.MapErr(err), state.ErrApplicationStandardRuntimeStale) {
		t.Fatalf("raw legacy grant escaped measured removal protocol: %v", err)
	}
	b.ProtocolVersion = runtimeadmission.ArtifactProtocolVersion
	b.ArtifactSourcesHash, err = state.StandardCapturedArtifactSourceHash(capture)
	if err != nil {
		t.Fatal(err)
	}
	if err := insert(); err != nil {
		t.Fatal("refused protocol left partial authority", err)
	}
	if _, err := s.PublishInstanceApplicationStandardRuntime(t.Context(), ins.State, state.StateRunning, state.ConsumedNativeReceipt(b, capture)); err != nil {
		t.Fatal("raw measured authority could not publish restored controls", err)
	}
}

func TestPgApplicationStandardRetainedNativeReenrollment(t *testing.T) {
	s, pool := state.RuntimeCapturePGStore(t)
	app, _, before := retainedNativeFixture(t, s)
	project, err := s.CreateProject(t.Context(), state.Project{AccountID: app.AccountID, Slug: "retained-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `UPDATE apps SET project_id=$1 WHERE id=$2`, project.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	app.ProjectID = project.ID
	after, err := s.GetApplicationStandardEnrollment(t.Context(), app.OrgID, app.ID)
	if err != nil || after.State != "pending" || after.DesiredRevision != before.DesiredRevision+1 || after.PersistedRevision != before.PersistedRevision || after.EffectiveHash != before.EffectiveHash || !state.ApplicationStandardEnrollmentRequiresNative(after) || state.ApplicationStandardEnrollmentPermitsRuntime(app, after) {
		t.Fatalf("scope change erased native history or permitted stale inputs: %+v %v", after, err)
	}
	_, err = pool.Exec(t.Context(), `UPDATE app_application_standards SET persisted_revision=0 WHERE app_id=$1`, app.ID)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "application_standard_enrollment_generation" {
		t.Fatalf("raw SQL erased installed native history: %v", err)
	}
}
