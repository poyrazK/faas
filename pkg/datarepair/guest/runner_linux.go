//go:build linux

// Package guest runs the fixed PostgreSQL rehearsal inside a disposable guest.
// It never connects to the source database or inherits application environment.
package guest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	core "github.com/onebox-faas/faas/pkg/datarepair"
)

const postgresBin = "/usr/lib/postgresql/16/bin"
const postgresUID = 999

func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, filepath.Join(postgresBin, name), args...)
	cmd.Env = []string{"PATH=" + postgresBin + ":/usr/bin:/bin", "LANG=C.UTF-8", "HOME=/nonexistent"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: &syscall.Credential{Uid: postgresUID, Gid: postgresUID}}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd
}

func run(ctx context.Context, input io.Reader, name string, args ...string) error {
	cmd := command(ctx, name, args...)
	cmd.Stdin = input
	return cmd.Run()
}

// Run expects an empty, private, size-capped workspace installed by guest-init.
// All tool output is discarded: database errors may include sensitive values.
func Run(parent context.Context, in core.RehearsalInput, workspace string) (result Result) {
	fail := func(stage string) Result { return Result{FailureCode: stage} }
	if in.Validate() != nil {
		return fail("invalid_input")
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	if err := os.Chown(workspace, postgresUID, postgresUID); err != nil {
		return fail("workspace")
	}
	data, socket := filepath.Join(workspace, "data"), filepath.Join(workspace, "socket")
	if os.Mkdir(socket, 0o700) != nil || os.Chown(socket, postgresUID, postgresUID) != nil {
		return fail("workspace")
	}
	if run(ctx, nil, "initdb", "-D", data, "--no-locale", "--encoding=UTF8", "--auth=trust", "--username=postgres") != nil {
		return fail("database_init")
	}
	server := command(ctx, "postgres", "-D", data, "-k", socket, "-c", "listen_addresses=", "-c", "shared_buffers=32MB", "-c", "max_connections=4", "-c", "max_worker_processes=0", "-c", "jit=off", "-c", "max_wal_size=128MB")
	if server.Start() != nil {
		return fail("database_start")
	}
	defer func() {
		if err := syscall.Kill(-server.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			result = fail("database_cleanup")
			return
		}
		_ = server.Wait()
	}()
	for run(ctx, nil, "pg_isready", "-h", socket, "-U", "postgres") != nil {
		select {
		case <-ctx.Done():
			return fail("database_start")
		case <-time.After(20 * time.Millisecond):
		}
	}
	admin := "repair_admin_" + uuid.NewString()[:8]
	if run(ctx, nil, "psql", "-h", socket, "-U", "postgres", "-d", "postgres", "-X", "-v", "ON_ERROR_STOP=1", "-q", "-c", "CREATE DATABASE "+admin) != nil {
		return fail("database_prepare")
	}
	if run(ctx, bytes.NewReader(in.Dump), "psql", "-h", socket, "-U", "postgres", "--dbname="+admin, "-X", "-v", "ON_ERROR_STOP=1", "-q") != nil {
		return fail("snapshot_restore")
	}
	cfg, err := pgxpool.ParseConfig("postgres://postgres@localhost/postgres?sslmode=disable")
	if err != nil {
		return fail("database_connect")
	}
	cfg.ConnConfig.Host, cfg.ConnConfig.Database = socket, in.Database
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return fail("database_connect")
	}
	defer pool.Close()
	// This private clone alone bypasses consent. The original plan identity and
	// row/version fences are then exercised by the same Apply implementation.
	if _, err = pool.Exec(ctx, `UPDATE gregale_repair.plans SET rehearsed_at=clock_timestamp(),approved_at=clock_timestamp(),approved_by='isolated-rehearsal' WHERE id=$1::uuid AND digest=$2`, in.Plan.ID, in.Plan.Digest); err != nil {
		return fail("candidate_apply")
	}
	engine := core.Engine{Pool: pool, CheckSQL: in.CheckSQL, Checks: in.Checks}
	applied, err := engine.Apply(ctx, in.Plan.Scope, in.Plan.ID, in.Plan.Digest, uuid.NewString(), "isolated-rehearsal")
	evidence := &core.Evidence{Passed: true, PlanDigest: in.Plan.Digest, SnapshotSHA256: in.SnapshotSHA256, ChecksSHA256: in.Plan.ChecksSHA256, Runner: core.MicroVMRunner}
	if err != nil {
		var failed *core.CheckError
		if errors.As(err, &failed) {
			evidence.Passed, evidence.CheckResults = false, failed.Report
			return Result{Evidence: evidence}
		}
		return fail("candidate_apply_and_checks")
	}
	evidence.CheckResults = applied.ApplyChecks
	return Result{Evidence: evidence}
}
