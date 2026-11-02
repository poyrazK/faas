//go:build metal && linux

// adr: 1027 — a native snapshot outlives the source's retention: after a
// newer base backup and a prune past the snapshot's point, restoring the
// snapshot still recovers exactly the commits before it; once deleted,
// pruning takes what it held.
package firecracker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestMetalSnapshotRestoreOutlivesRetention(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	p, connect := st.p, connectThroughProxy(st.proxyAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	source, target := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		_ = st.sched.Delete(context.Background(), target)
		_ = st.sched.Delete(context.Background(), source)
	})
	spec := devSpec()
	spec.RestoreWindowSeconds = 3600
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: source, Spec: spec}); err != nil {
		t.Fatal(err)
	}
	// query runs one statement, or with exec a batch, as the migration
	// role.
	run := func(id, sql string, exec bool) string {
		t.Helper()
		m, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: "mig", Access: managedpostgres.CredentialMigration})
		if err != nil {
			t.Fatal(err)
		}
		conn, err := connect(ctx, m)
		if err != nil {
			t.Fatalf("connect to %s: %v", id, err)
		}
		defer func() { _ = conn.Close(ctx) }()
		var out string
		if exec {
			_, err = conn.Exec(ctx, sql)
		} else {
			err = conn.QueryRow(ctx, sql).Scan(&out)
		}
		if err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return out
	}
	query := func(id, sql string) string { return run(id, sql, false) }
	run(source, "CREATE TABLE kept(v text); INSERT INTO kept VALUES ('before')", true)
	time.Sleep(1100 * time.Millisecond)
	point := time.Now().UTC().Truncate(time.Microsecond)
	time.Sleep(1100 * time.Millisecond)
	run(source, "INSERT INTO kept VALUES ('after')", true)

	capture := managedpostgres.SnapshotCaptureRequest{ResourceID: "clone/" + source, SourceResourceID: source, IdempotencyKey: "capture", PointInTime: point}
	started := time.Now()
	snap, err := p.CaptureSnapshot(ctx, capture)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	captured := time.Since(started)

	// A newer base backup, then a prune whose retention starts after it:
	// without the hold it would anchor on the new backup and drop the old
	// one, and the point with it.
	if resp, err := st.rt.Control(ctx, source, dbguest.Request{Op: dbguest.OpBaseBackup}); err != nil || resp.Error != "" {
		t.Fatalf("second base backup: %+v %v", resp, err)
	}
	backups, err := st.archive.ListBackups(source)
	if err != nil || len(backups) < 2 {
		t.Fatalf("backups = %+v, %v; want at least two", backups, err)
	}
	newest := backups[len(backups)-1]
	if _, err := st.archive.Prune(ctx, source, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("prune under the hold: %v", err)
	}
	if m, err := st.archive.SelectBackup(ctx, st.archive, source, point); err != nil || m.ID == newest.ID {
		t.Fatalf("backup for the point after the prune = %+v, %v; want an older one kept", m, err)
	}

	definition := managedpostgres.RestoreSourceDefinition{Spec: spec, BackendID: "native", BackendFingerprint: "fp", ProviderResourceID: source, DataResourceID: source}
	req := managedpostgres.SnapshotRestoreRequest{ResourceID: target, ProviderSnapshotID: snap.ProviderSnapshotID, Snapshot: capture}
	started = time.Now()
	obs, err := p.RestoreSnapshot(ctx, definition, req)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	req.ExpectedTargetResourceID = obs.ProviderResourceID
	for !obs.Restored {
		time.Sleep(time.Second)
		if obs, err = p.FindSnapshotRestore(ctx, definition, req); err != nil {
			t.Fatalf("find restore: %v", err)
		}
	}
	restored := time.Since(started)
	if got := query(target, "SELECT string_agg(v, ',' ORDER BY v) FROM kept"); got != "before" {
		t.Fatalf("restored rows = %q; want only the commit before the snapshot", got)
	}

	deletion := managedpostgres.SnapshotRestoreDeletionRequest{Restore: req, SnapshotCreatedAt: snap.CreatedAt, TargetCreatedAt: obs.TargetCreatedAt}
	if gone, err := p.DeleteSnapshotRestore(ctx, definition, deletion); err != nil || !gone.Done {
		t.Fatalf("delete restore = %+v, %v", gone, err)
	}
	if got := query(source, "SELECT string_agg(v, ',' ORDER BY v) FROM kept"); got != "after,before" {
		t.Fatalf("source rows after the clone's cleanup = %q", got)
	}
	del := managedpostgres.SnapshotDeleteRequest{ResourceID: capture.ResourceID, ProviderSnapshotID: snap.ProviderSnapshotID, SourceResourceID: source, PointInTime: point}
	if res, err := p.DeleteSnapshot(ctx, del); err != nil || !res.Done {
		t.Fatalf("delete snapshot = %+v, %v", res, err)
	}
	if _, err := st.archive.Prune(ctx, source, time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("prune without the hold: %v", err)
	}
	if left, err := st.archive.ListBackups(source); err != nil || len(left) != 1 || left[0].ID != newest.ID {
		t.Fatalf("backups without the hold = %+v, %v; want only the newest", left, err)
	}
	t.Logf("capture (hold + WAL flush) %v; restore from the snapshot %v", captured.Round(time.Millisecond), restored.Round(time.Millisecond))
}
