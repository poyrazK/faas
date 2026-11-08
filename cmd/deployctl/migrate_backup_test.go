package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

// TestMigrateBacksUpBeforePendingMigrations is issue #579: with migrations
// pending, a host that has the backup units takes and ships a base backup
// before migrating, and refuses to migrate when the backup fails; with none
// pending, or on a host without the units, it migrates directly.
func TestMigrateBacksUpBeforePendingMigrations(t *testing.T) {
	status := func(pending string) string {
		return `{"time":"t","level":"INFO","msg":"migration status","mode":"apply","db_version":5,"pending_count":` + pending + `}` + "\n"
	}
	for name, tc := range map[string]struct {
		status      string
		backupUnits bool
		failUnit    string
		wantCalls   []string
		wantErr     string
	}{
		"pending, backed up first": {status: status("2"), backupUnits: true,
			wantCalls: []string{"systemctl start faas-pg-basebackup.service", "systemctl start faas-pg-basebackup-push.service", "migrate"}},
		"nothing pending, no backup": {status: status("0"), backupUnits: true, wantCalls: []string{"migrate"}},
		"no backup units, migrates":  {status: status("3"), wantCalls: []string{"migrate"}},
		"backup fails, no migration": {status: status("1"), backupUnits: true, failUnit: "faas-pg-basebackup-push.service",
			wantCalls: []string{"systemctl start faas-pg-basebackup.service", "systemctl start faas-pg-basebackup-push.service"},
			wantErr:   "refusing to apply 1 pending migrations"},
		"status unreadable": {status: "not json\n", backupUnits: true, wantErr: "printed no pending_count"},
	} {
		t.Run(name, func(t *testing.T) {
			release, unitDir := t.TempDir(), t.TempDir()
			if err := os.MkdirAll(filepath.Join(release, "bin"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(release, "bin", "migrate"), []byte("#!/bin/sh\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(release, "systemd"), 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.backupUnits {
				for _, unit := range preMigrationBackupUnits {
					if err := os.WriteFile(filepath.Join(unitDir, unit), []byte("[Service]\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			origOutput, origRun := commandOutput, runCommand
			t.Cleanup(func() { commandOutput, runCommand = origOutput, origRun })
			var calls []string
			commandOutput = func(_ context.Context, name string, args ...string) (string, error) {
				if name != "/bin/bash" || !strings.Contains(strings.Join(args, " "), "-status") {
					t.Fatalf("unexpected output command %s %v", name, args)
				}
				return tc.status, nil
			}
			runCommand = func(_ context.Context, name string, args ...string) error {
				if name == "/bin/bash" && strings.Contains(strings.Join(args, " "), "/bin/migrate") {
					calls = append(calls, "migrate")
					return nil
				}
				call := name + " " + strings.Join(args, " ")
				calls = append(calls, call)
				if tc.failUnit != "" && strings.HasSuffix(call, tc.failUnit) {
					return errors.New("unit failed")
				}
				return nil
			}
			r := hostRuntime{unitDir: unitDir, databaseURL: "postgres://migrate"}
			err := r.Migrate(context.Background(), releasebundle.Manifest{Target: "linux/amd64"}, release, "")
			if tc.wantErr == "" && err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Migrate err = %v, want %q", err, tc.wantErr)
			}
			if !reflect.DeepEqual(calls, tc.wantCalls) {
				t.Fatalf("calls = %v, want %v", calls, tc.wantCalls)
			}
		})
	}
}

// TestMigrateRunsAsRootNotTheFaasUser: the owner role is reachable only from
// root once faas_postgres_owner_contracted drops `faas_map faas faas`
// (ADR-20261023104734), so neither the status check nor the migration may
// switch to the faas OS user (rc.330 failed its control-plane activation on
// "Peer authentication failed for user faas").
func TestMigrateRunsAsRootNotTheFaasUser(t *testing.T) {
	release := t.TempDir()
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(release, "bin", "migrate"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(release, "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	origOutput, origRun := commandOutput, runCommand
	t.Cleanup(func() { commandOutput, runCommand = origOutput, origRun })
	var seen []string
	commandOutput = func(_ context.Context, name string, args ...string) (string, error) {
		seen = append(seen, name+" "+strings.Join(args, " "))
		return `{"msg":"migration status","pending_count":0}` + "\n", nil
	}
	runCommand = func(_ context.Context, name string, args ...string) error {
		seen = append(seen, name+" "+strings.Join(args, " "))
		return nil
	}
	r := hostRuntime{unitDir: t.TempDir(), databaseURL: "postgres:///faas?host=/run/postgresql&user=faas"}
	if err := r.Migrate(context.Background(), releasebundle.Manifest{Target: "linux/amd64"}, release, ""); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("commands = %q, want the status check and the migration", seen)
	}
	for _, call := range seen {
		if strings.HasPrefix(call, "su ") || strings.Contains(call, " faas -s ") || strings.HasPrefix(call, "runuser ") || strings.HasPrefix(call, "sudo ") {
			t.Errorf("%q switches users; the migrator must run as root (faas_map root faas)", call)
		}
		if !strings.Contains(call, `DATABASE_URL="postgres:///faas?host=/run/postgresql&user=faas"`) {
			t.Errorf("%q does not pass the owner DATABASE_URL", call)
		}
	}
}
