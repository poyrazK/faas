package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresRealTaskObserverMatchesOnlyLiveClaim(t *testing.T) {
	f := newTaskPostgresFixture(t)
	ctx := t.Context()
	if _, err := f.owner.Exec(ctx, `
		ALTER TABLE gregale_mcp_tasks
		  ADD COLUMN namespace text, ADD COLUMN task_id uuid, ADD COLUMN tool_name text,
		  ADD COLUMN handler_version text, ADD COLUMN status text, ADD COLUMN attempt_count int,
		  ADD COLUMN lease_token uuid, ADD COLUMN lease_expires_at timestamptz,
		  ADD COLUMN expires_at timestamptz, ADD COLUMN cancel_requested_at timestamptz;
		ALTER TABLE gregale_mcp_task_workers ADD COLUMN namespace text, ADD COLUMN worker_id uuid,
		  ADD COLUMN expires_at timestamptz, ADD COLUMN draining boolean;
		ALTER TABLE gregale_mcp_task_worker_handlers ADD COLUMN namespace text, ADD COLUMN worker_id uuid,
		  ADD COLUMN tool_name text, ADD COLUMN handler_version text, ADD COLUMN deployment_id text;
	`); err != nil {
		t.Fatal(err)
	}
	run, err := uuid.Parse(f.resource.RunID)
	if err != nil {
		t.Fatal(err)
	}
	namespace := "rehearsal-" + run.String()
	taskID, workerID, deploymentID, token := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := f.writer.Exec(ctx, `INSERT INTO gregale_mcp_tasks
		(item, namespace, task_id, tool_name, handler_version, status, attempt_count, lease_token, lease_expires_at, expires_at)
		VALUES ('sample', $1, $2, 'build_report', '1', 'running', 1, $3, now() + interval '1 min', now() + interval '5 min')`,
		namespace, taskID, token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.writer.Exec(ctx, `INSERT INTO gregale_mcp_task_workers
		VALUES ('sample', $1, $2, now() + interval '1 min', false)`, namespace, workerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.writer.Exec(ctx, `INSERT INTO gregale_mcp_task_worker_handlers
		VALUES ('sample', $1, $2, 'build_report', '1', $3)`, namespace, workerID, deploymentID); err != nil {
		t.Fatal(err)
	}
	connection := func(role string, password string) string {
		dsn := url.URL{Scheme: "postgresql", Host: "observer.svc.gregale:5432",
			User: url.UserPassword(role, password), Path: "/" + f.resource.DatabaseName}
		return dsn.String()
	}
	config := f.observer.Config()
	r := f.resource
	observe, err := f.service.TaskObserver(ctx, r.AccountID, r.RunID, f.observerApp, connection(config.User, config.Password))
	if err != nil {
		t.Fatal(err)
	}
	writer := f.writer.Config()
	if _, err := f.service.TaskObserver(ctx, r.AccountID, r.RunID, f.runtimeApp, connection(writer.User, writer.Password)); err == nil {
		t.Fatal("writer credentials accepted as observer")
	}
	value := sha256.Sum256([]byte(token))
	claim := TaskClaim{TaskID: taskID, DeploymentID: deploymentID, WorkerID: workerID, LeaseSHA256: hex.EncodeToString(value[:]), Attempt: 1}
	for _, test := range []struct {
		name string
		edit func(*TaskClaim)
		want bool
	}{
		{"exact", func(*TaskClaim) {}, true},
		{"wrong task", func(c *TaskClaim) { c.TaskID = uuid.NewString() }, false},
		{"wrong deployment", func(c *TaskClaim) { c.DeploymentID = uuid.NewString() }, false},
		{"wrong worker", func(c *TaskClaim) { c.WorkerID = uuid.NewString() }, false},
		{"wrong lease", func(c *TaskClaim) { c.LeaseSHA256 = hex.EncodeToString(make([]byte, 32)) }, false},
		{"wrong attempt", func(c *TaskClaim) { c.Attempt = 2 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := claim
			test.edit(&c)
			got, err := observe(t.Context(), c)
			if got != test.want || err != nil {
				t.Fatalf("observe = %t, %v, want %t", got, err, test.want)
			}
		})
	}
	if _, err := f.writer.Exec(ctx, "UPDATE gregale_mcp_tasks SET lease_expires_at = now()"); err != nil {
		t.Fatal(err)
	}
	if got, err := observe(ctx, claim); got || err != nil {
		t.Fatalf("expired claim observed: %t %v", got, err)
	}
}
