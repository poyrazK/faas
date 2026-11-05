package datapipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSetupDataAPISourceRetryPersistsNewIntentAndPreviousDeployment(t *testing.T) {
	service, store, p, _ := schemaPipelineFixture(t)
	ctx := t.Context()
	if _, err := store.pool.Exec(ctx, `UPDATE data_pipelines SET state='live',schema_change='{}',reason='',observed_revision=revision,observed_at=clock_timestamp() WHERE id=$1`, p.ID); err != nil {
		t.Fatal(err)
	}
	app, binding, previous, previousBuild := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	service.PlanDataAPI = func(context.Context, string, SetupRequest) (DataAPIPlan, error) {
		return DataAPIPlan{AppID: app, BindingID: binding, PreviousDeploymentID: previous, PreviousBuildID: previousBuild}, nil
	}
	service.PrepareDataAPI = func(context.Context, Setup) (string, bool, error) { return binding, true, nil }
	changed := true
	submissions := 0
	service.DeployDataAPI = func(context.Context, Setup) (string, string, error) {
		submissions++
		if changed {
			return "", "", ErrDataAPISourceChanged
		}
		return uuid.NewString(), uuid.NewString(), nil
	}
	service.DataAPIState = func(context.Context, Setup) (string, string, error) { return "live", "https://data.example", nil }
	job, err := service.StartSetup(ctx, p.AccountID, SetupRequest{Name: "source-upgrade", SourceConnectionID: p.SourceConnectionID, DatabaseID: p.DatabaseID, Tables: p.Tables, ReadSubjects: p.ReadSubjects, DataAPIName: "source-upgrade-api", AuthAppID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipeline_setups SET phase='deploying_api',pipeline_id=$2 WHERE id=$1`, job.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	initial := job.DataAPIPlan.IntentID
	job = setupTick(t, service, store, job.ID)
	if job.State != "blocked" || job.Phase != "api_failed" || job.Reason != "data_api_source_changed" || job.DataAPIPlan.IntentID != initial {
		t.Fatal("archive conflict changed intent without explicit retry", job)
	}
	if _, err = store.ControlSetup(ctx, job.AccountID, job.ID, job.Revision+1, "retry"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = store.ControlSetup(ctx, job.AccountID, job.ID, job.Revision, "retry"); err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	if submissions != 1 || job.Phase != "deploying_api" || job.DataAPIPlan.IntentID == initial || job.DataAPIPlan.AppID != app || job.DataAPIPlan.BindingID != binding || job.DatabaseID != p.DatabaseID || job.PipelineID != p.ID || job.DataAPIPlan.PreviousDeploymentID != previous || job.DataAPIPlan.PreviousBuildID != previousBuild {
		t.Fatal("source retry replaced resources, lost lineage or submitted before persisting", job)
	}
	changed = false
	service.Store = NewPostgresStore(store.pool)
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "waiting_api" || submissions != 2 {
		t.Fatal("restarted worker did not resume fresh build intent", job)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "ready" || job.DataAPIPlan.Endpoint != "https://data.example" {
		t.Fatal("source retry never completed", job)
	}
}
