//go:build linux

// adr: 20261022090000 — native builder export reads only an exited enrolled builder.
package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// exitedBuilderJournal prepares a builder enrolled by this test process, then
// revokes it and confirms exit, as native retirement does before export.
func exitedBuilderJournal(t *testing.T, enroll bool) (*nativeLoopMountJournal, nativeLaunchRecord, *nativeLoopBackendFixture) {
	t.Helper()
	loops, owner, b := builderJournal(t, enroll)
	return loops, retireLoopFixtureOwner(t, loops, owner), b
}

// builderJournal prepares a builder, enrolled by this test process when
// enroll is set, without revoking it.
func builderJournal(t *testing.T, enroll bool) (*nativeLoopMountJournal, nativeLaunchRecord, *nativeLoopBackendFixture) {
	t.Helper()
	j := nativeJournalFixture(filepath.Join(t.TempDir(), "journal"))
	lease := leaseForSlot("build-0001", 3)
	lease.IsBuilder, lease.Plan = true, api.PlanHobby
	ctx := t.Context()
	if enroll {
		producer, err := nativeCurrentProducer(nativeBuilderExportProducerProtocol)
		if err != nil {
			t.Fatal(err)
		}
		ctx = context.WithValue(ctx, nativeProducerContextKey{}, producer)
	}
	if err := j.prepare(ctx, lease); err != nil {
		t.Fatal(err)
	}
	b := &nativeLoopBackendFixture{attachments: make(map[string]bool)}
	j.loopMounts = b
	loops := &nativeLoopMountJournal{owner: j, backend: b}
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	return loops, owner, b
}

func TestNativeBuilderExportReadsOnlyAndRetiresItsMount(t *testing.T) {
	loops, owner, b := exitedBuilderJournal(t, true)
	if owner.Version != 2 || owner.Producer.Protocol != nativeBuilderExportProducerProtocol {
		t.Fatalf("builder was not enrolled: %+v", owner)
	}
	b.exportTree = func(point string) error {
		for name, body := range map[string]string{
			"upper/etc/faas/build-done.json": `{"exit_code":0}`,
			"upper/build/out/image.tar":      "oci",
		} {
			path := filepath.Join(point, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
		}
		return nil
	}
	exportDir := filepath.Join(t.TempDir(), "build-0001")
	if err := os.Mkdir(exportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := loops.exportSession(t.Context(), owner, "/fixture.img", func(point string) error {
		records, err := loops.records(owner)
		if err != nil || len(records) != 1 || !records[0].ReadOnly || records[0].MountID != 124 || records[0].Removed {
			t.Fatalf("reader preceded a durable read-only frame: %+v %v", records, err)
		}
		return copyBuildExport(point, exportDir, 1<<20)
	}); err != nil {
		t.Fatal(err)
	}
	if b.readOnlyMounts != 1 || b.mounted != 1 {
		t.Fatalf("read-only mounts = %d of %d", b.readOnlyMounts, b.mounted)
	}
	for name, want := range map[string]string{"build-done.json": `{"exit_code":0}`, "build/out/image.tar": "oci"} {
		got, err := os.ReadFile(filepath.Join(exportDir, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatalf("export %s = %q %v", name, got, err)
		}
	}
	if err := loops.requireRemoved(owner); err != nil {
		t.Fatalf("export mount was not retired: %v", err)
	}
	if err := loops.retireAll(t.Context(), owner); err != nil {
		t.Fatalf("retirement after export: %v", err)
	}
}

func TestNativeBuilderExportRefusesUnexitedOrUnenrolledOwners(t *testing.T) {
	reader := func(string) error { t.Fatal("refused export invoked its reader"); return nil }

	loops, owner, b := exitedBuilderJournal(t, false)
	if err := loops.exportSession(t.Context(), owner, "/fixture.img", reader); err == nil || b.prepares != 0 {
		t.Fatalf("an unenrolled builder exported: err=%v prepares=%d", err, b.prepares)
	}

	// An enrolled builder whose exit is not confirmed may still be writing.
	loops, owner, b = builderJournal(t, true)
	if err := loops.exportSession(t.Context(), owner, "/fixture.img", reader); err == nil || b.prepares != 0 {
		t.Fatalf("an unexited builder exported: err=%v prepares=%d", err, b.prepares)
	}

	staging, stagingOwner, sb := nativeLoopJournalFixture(t)
	if err := staging.exportSession(t.Context(), retireLoopFixtureOwner(t, staging, stagingOwner), "/fixture.img", reader); err == nil || sb.prepares != 0 {
		t.Fatalf("an application VM exported build artifacts: err=%v prepares=%d", err, sb.prepares)
	}
}
