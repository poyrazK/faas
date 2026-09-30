//go:build !no_pg

package state_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-375: direct intent and maintenance writers serialize with the app locks
// held by the final clone publication transaction, including existing-key edits.
func TestPgCloneValuePublicationSerializesEveryWriter(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	for _, test := range []struct{ name, statement string }{
		{"variable_update", `update app_envs set value = 'edited' where app_id = $1 and scope = 'stage' and key = 'CAPTURED'`},
		{"variable_insert", `insert into app_envs(account_id, app_id, scope, key, value) select account_id, id, 'stage', 'EXTRA', 'edited' from apps where id = $1`},
		{"variable_delete", `delete from app_envs where app_id = $1 and scope = 'stage' and key = 'CAPTURED'`},
		{"secret_reseal", `update app_secrets set ciphertext = '\x656469746564' where app_id = $1 and scope = 'stage' and key = 'TOKEN'`},
		{"secret_delete", `delete from app_secrets where app_id = $1 and scope = 'stage' and key = 'TOKEN'`},
		{"route_update", `update project_environment_route_policies set declared_routes='[{"path":"/edited","methods":["GET"]}]' where app_id=$1 and environment_slug='stage'`},
		{"route_insert", `insert into project_environment_route_policies(account_id,project_id,app_id,environment_slug) select account_id,project_id,id,'stage' from apps where id=$1`},
		{"route_delete", `delete from project_environment_route_policies where app_id=$1 and environment_slug='stage'`},
		{"edge_update", `update project_environment_edge_policies set rules='[{"kind":"headers","match_path":"/","priority":100,"enabled":true,"action":{"kind":"headers","headers":{"response_headers":[{"name":"X-Edited","value":"edited","action":"set"}]}}}]' where app_id=$1 and environment_slug='stage'`},
		{"edge_insert", `insert into project_environment_edge_policies(account_id,project_id,app_id,environment_slug) select account_id,project_id,id,'stage' from apps where id=$1`},
		{"edge_delete", `delete from project_environment_edge_policies where app_id=$1 and environment_slug='stage'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			a, err := s.CreateAccount(ctx, test.name+"-value-fence@example.com", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := s.CreateProject(ctx, state.Project{AccountID: a.ID, Slug: "value-fence"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := s.CreateApp(ctx, state.App{AccountID: a.ID, ProjectID: project.ID, Slug: test.name + "-value-fence", Type: state.AppTypeApp})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertAppEnvInScope(ctx, a.ID, app.ID, "stage", "CAPTURED", "captured"); err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertAppSecretWithClassInScope(ctx, a.ID, app.ID, "stage", "TOKEN", "age1-captured", "1111111111111111", state.SecretClassPersistent, []byte("captured")); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: a.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutProjectEnvironmentRoutePolicy(ctx, state.ProjectEnvironmentRoutePolicy{AccountID: a.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "stage"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{AccountID: a.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "stage"}); err != nil {
				t.Fatal(err)
			}
			if test.name == "route_insert" {
				if _, err := pool.Exec(ctx, `delete from project_environment_route_policies where app_id=$1 and environment_slug='stage'`, app.ID); err != nil {
					t.Fatal(err)
				}
			}
			if test.name == "edge_insert" {
				if _, err := pool.Exec(ctx, `delete from project_environment_edge_policies where app_id=$1 and environment_slug='stage'`, app.ID); err != nil {
					t.Fatal(err)
				}
			}
			publication, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = publication.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := publication.Exec(ctx, `select id from apps where id = $1 for update`, app.ID); err != nil {
				t.Fatal(err)
			}
			var publicationPID int32
			if err := publication.QueryRow(ctx, `select pg_backend_pid()`).Scan(&publicationPID); err != nil {
				t.Fatal(err)
			}
			writer, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Release()
			var writerPID int32
			if err := writer.QueryRow(ctx, `select pg_backend_pid()`).Scan(&writerPID); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			var workers sync.WaitGroup
			workers.Add(1)
			go func() {
				defer workers.Done()
				tag, err := writer.Exec(ctx, test.statement, app.ID)
				if err == nil && tag.RowsAffected() != 1 {
					err = state.ErrConflict
				}
				result <- err
			}()
			defer func() { cancel(); _ = publication.Rollback(context.WithoutCancel(ctx)); workers.Wait() }()
			for {
				var blocked bool
				if err := pool.QueryRow(ctx, `select $1::integer = any(pg_blocking_pids($2::integer))`, publicationPID, writerPID).Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				select {
				case err := <-result:
					t.Fatalf("writer bypassed the publication lock: %v", err)
				case <-ctx.Done():
					t.Fatal("writer never blocked behind publication")
				case <-time.After(5 * time.Millisecond):
				}
			}
			var value string
			if err := publication.QueryRow(ctx, `select value from app_envs where app_id = $1 and scope = 'stage' and key = 'CAPTURED'`, app.ID).Scan(&value); err != nil || value != "captured" {
				t.Fatalf("publication read changed values: %q, %v", value, err)
			}
			if err := publication.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-result; err != nil {
				t.Fatalf("ordered writer did not finish after publication: %v", err)
			}
		})
	}
}
