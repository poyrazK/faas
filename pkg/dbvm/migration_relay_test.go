package dbvm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/dbmigration"
	"github.com/onebox-faas/faas/pkg/fcvm"
)

func TestMigrationRelayPreservesItsBindingAcrossRuntimeRestart(t *testing.T) {
	const id = "2b7d3c5e-8f43-4c7a-9d0e-5a1f2b3c4d5e"
	const operation = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"
	const password = "fixture-only"
	root, err := os.MkdirTemp("/tmp", "pgmig")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	vm := &databaseVM{root: root, lease: fcvm.Lease{UID: os.Getuid(), GID: os.Getgid()}}
	cfg := RuntimeConfig{VolumeRoot: root}
	r := &Runtime{cfg: cfg, vms: map[string]*databaseVM{id: vm}}
	control, err := net.Listen("unix", r.udsPath(vm))
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	result := make(chan error, 4)
	go func() {
		for i, op := range []dbguest.Op{dbguest.OpMigrationStatus, dbguest.OpMigrationStart, dbguest.OpMigrationPrepare, dbguest.OpMigrationStatus} {
			conn, err := control.Accept()
			if err == nil {
				err = respondMigrationControl(conn, op, operation, i != 0)
			}
			result <- err
		}
	}()
	endpoint := ImportSource{Host: "source.example.com", Port: 5432}
	request := dbguest.Request{Op: dbguest.OpMigrationStart, Import: &dbguest.ImportSource{
		Database: "source", User: "source_role", Password: password, OperationID: operation,
		TargetDatabase: "customer", Owner: "customer_role",
	}}
	resp, err := r.ControlMigration(t.Context(), id, endpoint, request)
	if err != nil || !resp.MigrationFenced || (resp.MigrationProgress == nil || resp.MigrationProgress.State != dbmigration.Preparing) {
		t.Fatalf("start: %+v, %v", resp, err)
	}
	for range 2 {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(r.migrationBindingPath(id))
	if err != nil || bytes.Contains(raw, []byte(password)) {
		t.Fatalf("saved endpoint contains credentials or could not be read: %v", err)
	}
	if err := vm.migrationLn.Close(); err != nil {
		t.Fatal(err)
	}
	recovered := &databaseVM{root: root, lease: vm.lease}
	next := &Runtime{cfg: cfg, vms: map[string]*databaseVM{id: recovered}}
	next.restoreMigrationRelay(t.Context(), id, recovered)
	if recovered.migrationLn == nil {
		t.Fatal("migration socket was not recovered")
	}
	defer recovered.migrationLn.Close()
	if source := recovered.migrationSrc.Load(); source == nil || *source != endpoint {
		t.Fatal("source endpoint was not recovered")
	}
	request.Op = dbguest.OpMigrationPrepare
	resp, err = next.ControlMigration(t.Context(), id, endpoint, request)
	if err != nil || resp.MigrationProgress == nil || resp.MigrationProgress.Preparation == nil {
		t.Fatalf("prepare on recovered endpoint: %+v, %v", resp, err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	resp, err = next.ControlMigration(t.Context(), id, ImportSource{Host: "other.example.com", Port: 5432}, request)
	if err != nil || resp.Error != dbguest.ErrCodeImportOperationMismatch {
		t.Fatalf("changed endpoint: %+v, %v", resp, err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func respondMigrationControl(conn net.Conn, op dbguest.Op, operation string, exists bool) error {
	defer conn.Close()
	read := bufio.NewReader(conn)
	if _, err := read.ReadString('\n'); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, "OK 1000\n"); err != nil {
		return err
	}
	var req dbguest.Request
	if err := json.NewDecoder(read).Decode(&req); err != nil {
		return err
	}
	if req.Op != op || (op.IsMigrationMutation() && (req.MigrationPin == "" || req.Import.OperationID != operation)) {
		return fmt.Errorf("unexpected guest control %s", req.Op)
	}
	progress := &dbmigration.Progress{State: dbmigration.None}
	if exists {
		progress = &dbmigration.Progress{State: dbmigration.Preparing, OperationID: operation}
	}
	if op == dbguest.OpMigrationPrepare {
		progress.Preparation = &dbmigration.Preparation{State: dbmigration.Preparing}
	}
	return json.NewEncoder(conn).Encode(dbguest.Response{
		State: dbguest.StateReady, MigrationProgress: progress, MigrationFenced: exists,
	})
}
