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
				if name != "su" || !strings.Contains(strings.Join(args, " "), "-status") {
					t.Fatalf("unexpected output command %s %v", name, args)
				}
				return tc.status, nil
			}
			runCommand = func(_ context.Context, name string, args ...string) error {
				if name == "su" {
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
