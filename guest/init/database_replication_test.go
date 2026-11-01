// adr: 1027
package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Only the bridge's OS user reaches the replication role, only for
// physical replication, and nothing maps to the superuser.
func TestReplicationAuthenticationIsBridgeOnly(t *testing.T) {
	want := "local replication " + databaseReplicationRole + " peer map=replication\n"
	if !strings.Contains(databaseHBA, want) {
		t.Fatalf("pg_hba.conf lacks %q:\n%s", want, databaseHBA)
	}
	if strings.Index(databaseHBA, want) > strings.Index(databaseHBA, "local all all scram-sha-256") {
		t.Fatal("the replication line must come before the catch-all line")
	}
	var maps []string
	for _, line := range strings.Split(databaseIdent, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			maps = append(maps, line)
		}
	}
	if !slices.Equal(maps, []string{"replication nobody " + databaseReplicationRole, "import postgres " + databaseImportRole}) {
		t.Fatalf("pg_ident.conf maps %q", maps)
	}
}

func TestStandbySettings(t *testing.T) {
	got := strings.Join(standbySettings(true), " ")
	for _, want := range []string{
		"primary_conninfo=host=/run/gregale-replication user=gregale_replication application_name=gregale_standby",
		"primary_slot_name=gregale_standby", "hot_standby=on", "restore_command=" + databaseRestoreCommand,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("standby settings lack %q: %s", want, got)
		}
	}
	if strings.Contains(strings.Join(standbySettings(false), " "), "restore_command") {
		t.Error("a standby without an archive reads none")
	}
}

func TestReplicationStatements(t *testing.T) {
	sync := strings.Join(replicationStatements(dbguest.ReplicationSync), "\n")
	for _, want := range []string{"CREATE ROLE gregale_replication WITH LOGIN REPLICATION", "pg_create_physical_replication_slot('gregale_standby', true)",
		"ALTER SYSTEM SET synchronous_standby_names = 'gregale_standby'", "pg_reload_conf()"} {
		if !strings.Contains(sync, want) {
			t.Errorf("sync lacks %q", want)
		}
	}
	async := strings.Join(replicationStatements(dbguest.ReplicationAsync), "\n")
	if !strings.Contains(async, "ALTER SYSTEM RESET synchronous_standby_names") || strings.Contains(async, "SET synchronous_standby_names =") {
		t.Errorf("async must reset synchronous_standby_names:\n%s", async)
	}
	off := strings.Join(replicationStatements(dbguest.ReplicationOff), "\n")
	if !strings.Contains(off, "pg_drop_replication_slot") || !strings.Contains(off, "AND NOT active") || strings.Contains(off, "CREATE ROLE") {
		t.Errorf("off must drop an idle slot and create nothing:\n%s", off)
	}
	for _, stmts := range [][]string{replicationStatements(dbguest.ReplicationSync), replicationStatements(dbguest.ReplicationOff)} {
		for _, stmt := range stmts {
			if strings.HasPrefix(stmt, "ALTER SYSTEM") && strings.Contains(stmt, ";") {
				t.Errorf("ALTER SYSTEM must run alone: %q", stmt)
			}
		}
	}
}

func TestParseReplicationStatus(t *testing.T) {
	cases := []struct {
		row  string
		want dbguest.ReplicationStatus
		err  bool
	}{
		{row: "|||-1|f", err: true},
		{row: "||-1||f", want: dbguest.ReplicationStatus{Mode: dbguest.ReplicationOff}},
		{row: "||-1||t", want: dbguest.ReplicationStatus{Mode: dbguest.ReplicationAsync}},
		{row: "streaming|async|8192||t", want: dbguest.ReplicationStatus{Mode: dbguest.ReplicationAsync, Streaming: true, LagBytes: 8192}},
		{row: "streaming|sync|0|gregale_standby|t\n", want: dbguest.ReplicationStatus{Mode: dbguest.ReplicationSync, Streaming: true, Synchronous: true}},
		// Sync configured but the standby still catching up is not synchronous.
		{row: "catchup|potential|4096|gregale_standby|t", want: dbguest.ReplicationStatus{Mode: dbguest.ReplicationSync}},
		{row: "streaming|sync|x|gregale_standby|t", err: true},
	}
	for _, tc := range cases {
		got, err := parseReplicationStatus(tc.row)
		if (err != nil) != tc.err || (err == nil && got != tc.want) {
			t.Errorf("parseReplicationStatus(%q) = %+v, %v; want %+v (err %v)", tc.row, got, err, tc.want, tc.err)
		}
	}
}

func TestSlotWALKeepMB(t *testing.T) {
	if got := slotWALKeepMB(256 << 20); got != 64 {
		t.Errorf("small volume keeps %d MB, want the 64 MB floor", got)
	}
	if got := slotWALKeepMB(100 << 30); got != 25600 {
		t.Errorf("100 GiB volume keeps %d MB, want a quarter", got)
	}
}

func TestPostmasterStatus(t *testing.T) {
	standby := "123\n/var/lib/postgresql/data\n1700000000\n5432\n/run/postgresql\n\n  5432001   0\nstandby   \n"
	if postmasterStatus([]byte(standby)) != "standby" || postmasterReady([]byte(standby)) {
		t.Fatal("a standby without hot_standby is standby, not ready")
	}
	if postmasterStatus([]byte("1\n2\n")) != "" {
		t.Fatal("a short pid file has no status")
	}
}

func TestStandbySeedUsesTheBridgeAndTheSlot(t *testing.T) {
	got := strings.Join(databaseStandbySeedArgs(17), " ")
	for _, want := range []string{"/usr/lib/postgresql/17/bin/pg_basebackup", "-h /run/gregale-replication", "-U gregale_replication",
		"-X stream", "-S gregale_standby", "-w"} {
		if !strings.Contains(got, want) {
			t.Errorf("seed args lack %q: %s", want, got)
		}
	}
}
