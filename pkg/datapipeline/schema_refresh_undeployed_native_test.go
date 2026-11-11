package datapipeline

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
)

func TestNativeSchemaRefreshDoesNotWaitForInitialAPIDeployment(t *testing.T) {
	f := newNativeRecoveryFixture(t, `CREATE TABLE public.orders(id int PRIMARY KEY,value text);INSERT INTO public.orders VALUES(1,'before')`)
	ctx := t.Context()
	schema, err := os.ReadFile("catalog_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	app := uuid.NewString()
	if _, err = f.store.pool.Exec(ctx, `INSERT INTO apps VALUES($1,$2,'initial-data-api','active')`, app, f.pipeline.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.pool.Exec(ctx, `INSERT INTO managed_postgres_bindings VALUES($1,$2,$3,$4,'default','read_only','ready')`, uuid.NewString(), f.pipeline.AccountID, f.pipeline.DatabaseID, app); err != nil {
		t.Fatal(err)
	}
	f.engine.RefreshSchema = f.store.RefreshSchema
	if _, err = f.source.Exec(ctx, `ALTER TABLE public.orders ADD COLUMN note text`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.Reason == "schema_change_pending" })
	if err = f.service.ApproveSchema(ctx, f.pipeline.AccountID, f.pipeline.ID, f.pipeline.Revision, f.pipeline.SchemaChange.ID, false); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(p Pipeline) bool { return p.State == "live" && p.SchemaChange.State == "complete" })
	refreshes := f.pipeline.SchemaChange.Refreshes
	if len(refreshes) != 1 || refreshes[0].Status != "not_required" {
		t.Fatal("initial API blocked native schema completion", refreshes)
	}
	var pending int
	if err = f.store.pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("unstartable API restart was queued", pending, err)
	}
	if _, err = f.source.Exec(ctx, `UPDATE public.orders SET note='continued' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	awaitNativeWorkflow(t, f, func(Pipeline) bool {
		var note string
		return f.target.QueryRow(ctx, `SELECT note FROM public.orders WHERE id=1`).Scan(&note) == nil && note == "continued"
	})
	deployed := false
	f.service.PrepareDataAPI = func(context.Context, Setup) (string, bool, error) { return "", true, nil }
	f.service.DeployDataAPI = func(context.Context, Setup) (string, string, error) {
		deployed = true
		return uuid.NewString(), uuid.NewString(), nil
	}
	f.service.DataAPIState = func(context.Context, Setup) (string, string, error) { return "building", "", nil }
	job := Setup{AccountID: f.pipeline.AccountID, PipelineID: f.pipeline.ID, Phase: "deploying_api", DataAPIPlan: DataAPIPlan{AppID: app}}
	if err = f.service.advanceDataAPI(ctx, &job); err != nil || !deployed || job.Phase != "waiting_api" {
		t.Fatal("schema completion stranded API setup", job, err)
	}
}
