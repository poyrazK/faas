package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func isolateDevSeedConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

type fakeDevSeedAPI struct {
	bindingStates []string
	taskStates    []api.AppTaskStatus
	exitCode      *int
	created       []api.CreateAppTaskRequest
	createErr     error
}

func (f *fakeDevSeedAPI) ops() devSeedOps {
	return devSeedOps{
		getBinding: func(context.Context, string) (api.ManagedPostgresBinding, error) {
			state := f.bindingStates[0]
			if len(f.bindingStates) > 1 {
				f.bindingStates = f.bindingStates[1:]
			}
			return api.ManagedPostgresBinding{State: state}, nil
		},
		createTask: func(_ context.Context, _ string, req api.CreateAppTaskRequest) (api.AppTaskResponse, error) {
			f.created = append(f.created, req)
			if f.createErr != nil {
				return api.AppTaskResponse{}, f.createErr
			}
			return api.AppTaskResponse{ID: "task-1", Status: api.AppTaskStatusQueued}, nil
		},
		getTask: func(context.Context, string, string) (api.AppTaskResponse, error) {
			status := f.taskStates[0]
			if len(f.taskStates) > 1 {
				f.taskStates = f.taskStates[1:]
			}
			resp := api.AppTaskResponse{ID: "task-1", Status: status, StdoutTail: "inserted 3 rows\n"}
			if status.Terminal() {
				resp.ExitCode = f.exitCode
			}
			return resp, nil
		},
		pollInterval:   time.Millisecond,
		bindingTimeout: time.Second,
		taskTimeout:    time.Second,
	}
}

func devSeedSession(bindingState string) api.DevSessionResponse {
	return api.DevSessionResponse{
		App: api.AppResponse{Slug: "dev-api-0123456789ab"},
		Postgres: &api.DevPostgresResponse{
			DatabaseID: "db-0001", Name: "dev-api-0123456789ab", BindingID: "binding-1",
			BindingState: bindingState, EnvironmentKey: "DATABASE_URL",
		},
	}
}

func TestDevSeedMarkerRoundTrip(t *testing.T) {
	isolateDevSeedConfig(t)
	done, err := devSeedCompleted("db-0001")
	if err != nil || done {
		t.Fatalf("devSeedCompleted before record = %v, %v; want false, nil", done, err)
	}
	if err := recordDevSeedCompleted("db-0001", "npm run seed --token=secret", "task-1"); err != nil {
		t.Fatalf("record: %v", err)
	}
	done, err = devSeedCompleted("db-0001")
	if err != nil || !done {
		t.Fatalf("devSeedCompleted after record = %v, %v; want true, nil", done, err)
	}
	path, err := devSeedMarkerPath("db-0001")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret") || strings.Contains(string(body), "npm") {
		t.Fatalf("marker must store only a command digest, got %q", body)
	}
	if other, err := devSeedCompleted("db-0002"); err != nil || other {
		t.Fatalf("a recreated database must not inherit the marker: %v, %v", other, err)
	}
}

func TestDevSeedMarkerRejectsUnsafeDatabaseID(t *testing.T) {
	isolateDevSeedConfig(t)
	for _, id := range []string{"", "../escape", "a/b", strings.Repeat("a", 65)} {
		if _, err := devSeedMarkerPath(id); err == nil {
			t.Errorf("devSeedMarkerPath(%q) succeeded, want error", id)
		}
	}
}

func TestShouldRunDevSeed(t *testing.T) {
	isolateDevSeedConfig(t)
	pg := &api.DevPostgresResponse{DatabaseID: "db-0001"}
	cases := []struct {
		name     string
		command  string
		postgres *api.DevPostgresResponse
		reseed   bool
		seeded   bool
		want     bool
	}{
		{name: "no command", postgres: pg},
		{name: "no database", command: "seed"},
		{name: "new database", command: "seed", postgres: pg, want: true},
		{name: "already seeded", command: "seed", postgres: pg, seeded: true},
		{name: "reseed forces", command: "seed", postgres: pg, seeded: true, reseed: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateDevSeedConfig(t)
			if tc.seeded {
				if err := recordDevSeedCompleted(pg.DatabaseID, tc.command, "task"); err != nil {
					t.Fatal(err)
				}
			}
			got, err := shouldRunDevSeed(tc.command, tc.postgres, tc.reseed)
			if err != nil || got != tc.want {
				t.Fatalf("shouldRunDevSeed = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestRunDevPostgresSeedWaitsForBindingThenRunsShellTask(t *testing.T) {
	fake := &fakeDevSeedAPI{
		bindingStates: []string{"provisioning", "provisioning", "ready"},
		taskStates:    []api.AppTaskStatus{api.AppTaskStatusRunning, api.AppTaskStatusSucceeded},
	}
	session := devSeedSession("provisioning")
	task, err := runDevPostgresSeed(context.Background(), fake.ops(), session.App.Slug, session.Postgres, "npm run seed")
	if err != nil {
		t.Fatalf("runDevPostgresSeed: %v", err)
	}
	if task.Status != api.AppTaskStatusSucceeded {
		t.Fatalf("status = %s, want succeeded", task.Status)
	}
	if len(fake.created) != 1 || !fake.created[0].CommandShell || len(fake.created[0].Command) != 1 || fake.created[0].Command[0] != "npm run seed" {
		t.Fatalf("created tasks = %+v, want one shell task", fake.created)
	}
}

func TestRunDevPostgresSeedFailures(t *testing.T) {
	exit := 3
	cases := []struct {
		name string
		fake *fakeDevSeedAPI
		want string
	}{
		{
			name: "binding failed",
			fake: &fakeDevSeedAPI{bindingStates: []string{"failed"}},
			want: "binding is failed",
		},
		{
			name: "task failed",
			fake: &fakeDevSeedAPI{bindingStates: []string{"ready"}, taskStates: []api.AppTaskStatus{api.AppTaskStatusFailed}, exitCode: &exit},
			want: "exit_code=3",
		},
		{
			name: "task rejected",
			fake: &fakeDevSeedAPI{bindingStates: []string{"ready"}, createErr: errors.New("no live deployment")},
			want: "start database seed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := devSeedSession("provisioning")
			_, err := runDevPostgresSeed(context.Background(), tc.fake.ops(), session.App.Slug, session.Postgres, "npm run seed")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want substring %q", err, tc.want)
			}
			if tc.name == "binding failed" && len(tc.fake.created) != 0 {
				t.Fatalf("seed task must not start before the binding is ready")
			}
		})
	}
}

func TestRunDevSeedAfterLiveSyncSeedsOncePerDatabase(t *testing.T) {
	isolateDevSeedConfig(t)
	stdout, restoreStdout := captureStdout(t)
	t.Cleanup(restoreStdout)
	_, restoreStderr := captureStderr(t)
	t.Cleanup(restoreStderr)

	fake := &fakeDevSeedAPI{bindingStates: []string{"ready"}, taskStates: []api.AppTaskStatus{api.AppTaskStatusSucceeded}}
	session := devSeedSession("ready")
	var reported []devDiagnostic
	report := func(d devDiagnostic) { reported = append(reported, d) }

	if got := runDevSeedAfterLiveSyncWith(context.Background(), fake.ops(), session, "npm run seed", false, report); got != devSeedOutcomeDone {
		t.Fatalf("first seed outcome = %v, want done", got)
	}
	if got := runDevSeedAfterLiveSyncWith(context.Background(), fake.ops(), session, "npm run seed", false, report); got != devSeedOutcomeDone {
		t.Fatalf("second seed outcome = %v, want done", got)
	}
	if len(fake.created) != 1 {
		t.Fatalf("seed tasks = %d, want exactly one per database", len(fake.created))
	}
	if got := runDevSeedAfterLiveSyncWith(context.Background(), fake.ops(), session, "npm run seed", true, report); got != devSeedOutcomeDone {
		t.Fatalf("reseed outcome = %v, want done", got)
	}
	if len(fake.created) != 2 {
		t.Fatalf("seed tasks after --reseed = %d, want 2", len(fake.created))
	}
	if len(reported) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", reported)
	}
	out := stdout.String()
	for _, want := range []string{"seed | inserted 3 rows", "already seeded"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want substring %q", out, want)
		}
	}
}

func TestRunDevSeedAfterLiveSyncReportsFailureWithoutMarker(t *testing.T) {
	isolateDevSeedConfig(t)
	_, restoreStdout := captureStdout(t)
	t.Cleanup(restoreStdout)
	_, restoreStderr := captureStderr(t)
	t.Cleanup(restoreStderr)

	exit := 1
	fake := &fakeDevSeedAPI{bindingStates: []string{"ready"}, taskStates: []api.AppTaskStatus{api.AppTaskStatusFailed}, exitCode: &exit}
	session := devSeedSession("ready")
	var reported []devDiagnostic
	got := runDevSeedAfterLiveSyncWith(context.Background(), fake.ops(), session, "npm run seed", false, func(d devDiagnostic) {
		reported = append(reported, d)
	})
	if got != devSeedOutcomeFailed {
		t.Fatalf("outcome = %v, want failed", got)
	}
	if len(reported) != 1 || reported[0].Code != devDiagSeedFailed || reported[0].Phase != "seed" {
		t.Fatalf("diagnostics = %+v, want one developer_seed_failed", reported)
	}
	if done, _ := devSeedCompleted(session.Postgres.DatabaseID); done {
		t.Fatalf("a failed seed must not be recorded as completed")
	}
}

func TestCmdDevRejectsInvalidSeedFlags(t *testing.T) {
	isolateDevSeedConfig(t)
	t.Chdir(t.TempDir())
	cases := []struct {
		args []string
		want string
	}{
		{args: []string{"--postgres-seed", "npm run seed"}, want: "--postgres-seed requires --postgres"},
		{args: []string{"--postgres", "--reseed"}, want: "--reseed requires"},
		{args: []string{"--stop", "--postgres-seed", "npm run seed"}, want: "cannot be combined with --stop"},
		{args: []string{"--postgres", "--postgres-seed", "a\x00b"}, want: "Invalid --postgres-seed"},
	}
	for _, tc := range cases {
		stderr, restoreStderr := captureStderr(t)
		code := cmdDev(tc.args)
		restoreStderr()
		if code != 1 || !strings.Contains(stderr.String(), tc.want) {
			t.Errorf("cmdDev(%q) = %d, stderr %q; want 1 and substring %q", tc.args, code, stderr.String(), tc.want)
		}
	}
}
