//go:build linux || darwin

// ADR-435: native ownership and uncertain retirement must remain fenced.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func retiredNativeJournal(t *testing.T) (*nativeLaunchJournal, Lease, nativeLaunchRecord) {
	t.Helper()
	j, lease := preparedNativeJournal(t)
	record, err := j.retire(t.Context(), lease.Instance, nativeProcessRetirer{probe: nativeProcessProbe{root: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	return j, lease, record
}

func TestNativeJournalResourceAcknowledgementBindsFullIncarnation(t *testing.T) {
	for _, change := range []string{"generation", "boot", "pid", "start", "lease", "unretired", "success"} {
		t.Run(change, func(t *testing.T) {
			j, lease, record := retiredNativeJournal(t)
			switch change {
			case "generation":
				record.Generation = "de5b0c66-9c0d-4f4d-bb7d-d7fdcc6f7772"
			case "boot":
				record.KernelBootID = "de5b0c66-9c0d-4f4d-bb7d-d7fdcc6f7772"
			case "pid":
				record.PID++
			case "start":
				record.StartTime++
			case "lease":
				record.Lease.Networkless = true
			case "unretired":
				record.Revoked, record.ExitConfirmed = false, false
				if err := j.write(record); err != nil {
					t.Fatal(err)
				}
			}
			err := j.confirmResourcesRemoved(t.Context(), record)
			persisted, readErr := j.read(lease.Instance)
			if readErr != nil || (err == nil) != (change == "success") || persisted.ResourcesRemoved != (change == "success") {
				t.Fatalf("record=%+v confirmation=%v read=%v", persisted, err, readErr)
			}
		})
	}
}

func TestNativeJournalReplacementRequiresCompleteCleanupOrExactFallbackLease(t *testing.T) {
	j, lease, old := retiredNativeJournal(t)
	if _, err := j.replace(t.Context(), lease, old.Generation, true); err == nil {
		t.Fatal("reused slot without resource acknowledgement")
	}
	changed := lease
	changed.Slot++
	changed.UID++
	changed.GID++
	changed.VethHost, changed.VethPeer = "vh4", "vp4"
	if _, err := j.replace(t.Context(), changed, old.Generation, false); err == nil {
		t.Fatal("fallback changed its owned lease")
	}
	fresh, err := j.replace(t.Context(), lease, old.Generation, false)
	if err != nil || fresh.Generation == old.Generation || fresh.Authorized || fresh.Revoked || fresh.ExitConfirmed || fresh.ResourcesRemoved {
		t.Fatalf("fallback record=%+v err=%v", fresh, err)
	}
	archived, err := readNativeLaunchRecord(filepath.Join(j.root, "retired", old.Generation+".json"), lease.Instance)
	if err != nil || archived != old {
		t.Fatalf("archive=%+v err=%v", archived, err)
	}
	if _, err := j.replace(t.Context(), lease, old.Generation, false); err == nil {
		t.Fatal("old generation replaced fresh producer")
	}
}

func TestNativeJournalReplacementRecoversArchiveBeforePublishCrash(t *testing.T) {
	j, lease, old := retiredNativeJournal(t)
	if err := j.confirmResourcesRemoved(t.Context(), old); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("current record fsync failed")
	j.writeRecord = func(string, nativeLaunchRecord) error { return cause }
	if _, err := j.replace(t.Context(), lease, old.Generation, true); !errors.Is(err, cause) {
		t.Fatalf("first replacement=%v", err)
	}
	before, err := os.ReadFile(filepath.Join(j.root, "retired", old.Generation+".json"))
	if err != nil {
		t.Fatal(err)
	}
	j.writeRecord = nil
	fresh, err := j.replace(t.Context(), lease, old.Generation, true)
	if err != nil || fresh.Generation == old.Generation {
		t.Fatalf("retry record=%+v err=%v", fresh, err)
	}
	after, err := os.ReadFile(filepath.Join(j.root, "retired", old.Generation+".json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("retry rewrote immutable archive")
	}
}

func TestNativeJournalReplacementRefusesDifferentArchive(t *testing.T) {
	j, lease, old := retiredNativeJournal(t)
	archive := filepath.Join(j.root, "retired")
	if err := os.MkdirAll(archive, 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := old
	corrupt.Lease.MemoryMaxMiB++
	if err := writeNativeLaunchRecord(filepath.Join(archive, old.Generation+".json"), corrupt); err != nil {
		t.Fatal(err)
	}
	if _, err := j.replace(t.Context(), lease, old.Generation, false); err == nil {
		t.Fatal("borrowed another archived frame")
	}
	persisted, err := j.read(lease.Instance)
	if err != nil || persisted.Generation != old.Generation {
		t.Fatal("refused archive mutated producer")
	}
}

func TestNativeJournalEnumerationFailsClosedOnDamagedOrCanceledScan(t *testing.T) {
	j, _ := preparedNativeJournal(t)
	if records, err := j.records(t.Context()); err != nil || len(records) != 1 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := j.records(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled enumeration=%v", err)
	}
	if err := os.WriteFile(filepath.Join(j.root, "unexpected"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.records(t.Context()); err == nil {
		t.Fatal("damaged root granted complete ownership scan")
	}
}
