//go:build !no_pg

package state

// adr: 435. Raw SQL and Go gates must bind the same composed identity/findings.

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgNativeComposedPolicyAuthority(t *testing.T) {
	nativeComposedPolicyAuthority(t, func(t *testing.T) nativeArtifactTestStore { s, _ := runtimeCapturePGStore(t); return s })
}

func TestPgNativeComposedIdentityHashMatchesGo(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	_, _, app, dep := nativeArtifactFixture(t, s, true)
	inputs, err := s.GetFreshDeploymentRuntimeProducerInputs(t.Context(), app.AccountID, app.ID, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{inputs.Artifacts[1].StorageKey, "apps/a\"<>&.ext4", "apps/\u2028\u2029/雪.ext4"} {
		identity := inputs.deploymentRuntimeArtifactIdentity
		identity.Artifacts = append([]DeploymentRuntimeArtifact(nil), identity.Artifacts...)
		identity.Artifacts[1].StorageKey = key
		identity, hash, err := prepareRuntimeArtifactIdentity(identity)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(identity)
		if err != nil {
			t.Fatal(err)
		}
		var actual string
		if err := pool.QueryRow(t.Context(), `SELECT application_standard_runtime_identity_hash($1::jsonb)`, raw).Scan(&actual); err != nil || actual != hash {
			t.Fatal("SQL hash differs from canonical Go producer identity", actual, hash, err)
		}
	}
}

func TestPgNativeComposedRawDeadlineContracts(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	component, _, app, dep := nativeArtifactFixture(t, s, false)
	app = manageNativeArtifactApp(t, s, app)
	value := publishNativeComposedScan(t, s, app, dep, component.Report)
	identity, err := json.Marshal(value.Input.deploymentRuntimeArtifactIdentity)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := json.Marshal(value.Input)
	if err != nil {
		t.Fatal(err)
	}
	var deadline time.Time
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_native_composed_views_deadline($1::jsonb,$2::jsonb,clock_timestamp(),false)`, snapshot, identity).Scan(&deadline); err != nil {
		t.Fatal("composed view/report deadline", err)
	}
	ins, _ := nativeArtifactAttempt(t, s, app, dep)
	capture, err := s.GetInstanceApplicationStandardAdmission(t.Context(), ins.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range value.Input.Artifacts {
		raw, err := json.Marshal(artifact)
		if err != nil {
			t.Fatal(err)
		}
		if artifact.Kind == "base-image" {
			if _, err := pool.Exec(t.Context(), `SELECT application_standard_native_base_producer_current($1::jsonb,clock_timestamp())`, raw); err != nil {
				t.Fatal("current base producer", err)
			}
		} else if err := pool.QueryRow(t.Context(), `SELECT application_standard_native_producer_deadline($1::jsonb,$2::jsonb,clock_timestamp())`, capture.inputs, raw).Scan(&deadline); err != nil {
			t.Fatal("current signed producer deadline", err)
		}
	}
	if err := pool.QueryRow(t.Context(), `SELECT application_standard_native_artifact_deadline($1::jsonb,clock_timestamp())`, capture.inputs).Scan(&deadline); err != nil || !deadline.Equal(value.ExpiresAt) {
		t.Fatal("composed authority deadline", deadline, value.ExpiresAt, err)
	}
}

func TestPgNativeComposedRawAuthorityRejectsMalformedPrivateFacts(t *testing.T) {
	for _, mode := range []string{"wrong input hash", "wrong source hash", "missing view", "duplicate report", "understated finding count", "future tree", "projection mismatch"} {
		t.Run(mode, func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			component, _, app, dep := nativeArtifactFixture(t, s, false)
			app = manageNativeArtifactApp(t, s, app)
			in := nativeComposedScanInput(t, s, app, dep, component.Report)
			in, hash, err := prepareDeploymentRuntimeScan(in)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "wrong input hash":
				in.Facts.InputHash = component.RootfsInputHash
			case "wrong source hash":
				in.Facts.SourcesHash = component.RootfsInputHash
			case "missing view":
				in.Facts.Views = nil
			case "duplicate report":
				in.Reports = append(in.Reports, in.Reports[0])
			case "understated finding count":
				in.Reports[0].Report.Vulnerabilities[0].Severity = "HIGH"
			case "future tree":
				in.Facts.Views[0].SourceTree.Version++
			case "projection mismatch":
				in.Facts.Views[0].ProjectionTree.Bytes++
			}
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(t.Context())
			q := sqlc.New()
			if err := q.AuthorizeDeploymentRuntimeScanInsert(t.Context(), tx, mustPgUUID(in.ID)); err != nil {
				t.Fatal(err)
			}
			row, err := q.InsertDeploymentRuntimeScan(t.Context(), tx, sqlc.InsertDeploymentRuntimeScanParams{ID: mustPgUUID(in.ID), DeploymentID: mustPgUUID(dep.ID), InputSnapshot: raw, InputHash: hash, PublisherExpiresAt: standardPgTime(time.Now().Add(time.Minute)), TtlSeconds: 60, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
			if err != nil {
				t.Fatal(err)
			}
			if err := q.SelectDeploymentRuntimeScan(t.Context(), tx, sqlc.SelectDeploymentRuntimeScanParams{ID: row.ID, DeploymentID: row.DeploymentID}); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(t.Context()); err != nil {
				t.Fatal(err)
			}
			ins, candidate := nativeArtifactAttempt(t, s, app, dep)
			candidate.Token = uuid.NewString()
			candidate.ExpiresAtUnixNano = row.ExpiresAt.Time.UnixNano()
			raw, err = json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			_, err = pool.Exec(t.Context(), `INSERT INTO instance_application_standard_boots(token,instance_id,expected_state,binding) VALUES($1,$2,$3,$4::jsonb)`, candidate.Token, ins.ID, ins.State, raw)
			if !errors.Is(mapErr(err), ErrApplicationStandardRuntimeStale) {
				t.Fatal("raw native grant accepted malformed composed facts", mode, err)
			}
			assertNativeBootUnpublished(t, s, ins)
		})
	}
}
