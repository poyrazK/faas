//go:build linux && metal

// adr: 403
package fcvm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

type preparedCrashFixture struct {
	Phase, Journal, Ready, Source string
}

// Kill a separate journal-owning process with real nsfs/veth resources alive.
// Recovery must fence both aliases, including the two-bind-mount window.
func TestMetalResourcePreparedCrashRecovery(t *testing.T) {
	if cfg := os.Getenv("FAAS_TEST_PREPARED_CRASH_FIXTURE"); cfg != "" {
		var f preparedCrashFixture
		if err := json.Unmarshal([]byte(cfg), &f); err != nil {
			t.Fatal(err)
		}
		runPreparedCrashChild(t, f)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("requires root on dedicated Linux KVM")
	}
	for _, phase := range []string{"spare", "transfer", "two_aliases", "alias", "guest"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			f := preparedCrashFixture{phase, filepath.Join(dir, "journal"), filepath.Join(dir, "ready"), "prepared-" + uuid.NewString()}
			l := leaseForSlot(f.Source, 0)
			names := []string{l.Netns, "fc-" + idLive}
			t.Cleanup(func() {
				ctx := context.WithoutCancel(t.Context())
				run := wire.ExecRunner{}
				_ = run.Run(ctx, []string{"ip", "link", "del", l.VethHost})
				for _, name := range names {
					_ = run.Run(ctx, []string{"ip", "netns", "del", name})
				}
				leakcheck.AssertZero(t)
			})
			cfg, err := json.Marshal(f)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestMetalResourcePreparedCrashRecovery$", "-test.timeout=1m")
			cmd.Env = append(os.Environ(), "FAAS_TEST_PREPARED_CRASH_FIXTURE="+string(cfg))
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() { _ = cmd.Process.Kill() })
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			deadline := time.NewTimer(30 * time.Second)
			defer deadline.Stop()
		waitReady:
			for {
				select {
				case err := <-done:
					t.Fatalf("fixture exited before crash checkpoint: %v\n%s", err, output.String())
				case <-deadline.C:
					_ = cmd.Process.Kill()
					<-done
					t.Fatalf("fixture checkpoint timed out\n%s", output.String())
				case <-ticker.C:
					if _, err := os.Stat(f.Ready); err == nil {
						break waitReady
					}
				}
			}
			if other, err := OpenResourceJournal(f.Journal); err == nil {
				_ = other.Close()
				t.Fatal("child did not hold exclusive journal lock")
			}
			beforeLink, err := resourceNetworkLinkAt(l.VethHost, 0)
			if err != nil || beforeLink == nil {
				t.Fatal("fixture link missing", err)
			}
			beforeNS := make(map[string]*resourceAsset)
			for _, name := range names {
				a, err := resourceNetworkNamespaceAt(name)
				if err != nil {
					t.Fatal(err)
				}
				beforeNS[name] = a
			}
			if phase == "two_aliases" && (beforeNS[names[0]] == nil || beforeNS[names[1]] == nil || *beforeNS[names[0]].File != *beforeNS[names[1]].File) {
				t.Fatal("fixture did not reach dual nsfs alias window")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			var killed *exec.ExitError
			if err := <-done; !errors.As(err, &killed) || killed.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("fixture did not die by SIGKILL: %v", err)
			}
			j := openTestResourceJournal(t, f.Journal)
			m := newTestManager(wire.ExecRunner{}, &fakeVMM{})
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			rep, err := m.RecoverRestartQuarantine(t.Context(), t.TempDir())
			if err != nil || rep.JournalRecords != 1 || rep.Slots != 1 || m.LeasedCount() != 0 {
				t.Fatalf("crash inventory: %+v, %v", rep, err)
			}
			ids := []string{f.Source}
			if phase != "spare" {
				ids = append(ids, idLive)
			}
			for _, id := range ids {
				if m.HasInstanceOwnership(id) || !errors.Is(m.Destroy(t.Context(), id), ErrRestartQuarantine) {
					t.Fatal("reopened record authorized destructive recovery")
				}
				if _, err := m.Wake(t.Context(), WakeRequest{Instance: id}); !errors.Is(err, ErrRestartQuarantine) {
					t.Fatal("recovered identity admitted")
				}
			}
			fresh, err := m.alloc.reserveNetwork("new-spare")
			if err != nil || fresh.Slot == 0 {
				t.Fatal("surviving slot reused")
			}
			afterLink, err := resourceNetworkLinkAt(l.VethHost, beforeLink.Index)
			if err != nil || afterLink == nil || *afterLink != *beforeLink {
				t.Fatal("restart mutated physical host link", err)
			}
			for _, name := range names {
				after, err := resourceNetworkNamespaceAt(name)
				before := beforeNS[name]
				if err != nil || (before == nil) != (after == nil) || (before != nil && !namespaceCheckpointMatches(*before, *after)) {
					t.Fatal("restart mutated namespace alias", name, err)
				}
			}
			if _, err := j.dir.ReadFile(resourceRecordName(f.Source)); err != nil {
				t.Fatal("stable crash record missing", err)
			}
		})
	}
}

func runPreparedCrashChild(t *testing.T, f preparedCrashFixture) {
	t.Helper()
	j := openTestResourceJournal(t, f.Journal)
	m := newTestManager(wire.ExecRunner{}, &fakeVMM{})
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	l, err := m.alloc.reserveNetwork(f.Source)
	if err != nil {
		t.Fatal(err)
	}
	nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
	if err := j.beginPrepared(l); err != nil {
		t.Fatal(err)
	}
	creator, err := resourcePlacementContext()
	if err != nil {
		t.Fatal(err)
	}
	if err := j.addAsset(l.Instance, resourceAsset{Kind: "netns", Path: filepath.Join("/run/netns", nc.Netns), Namespace: creator}); err != nil {
		t.Fatal(err)
	}
	if err := m.run.Run(t.Context(), []string{"ip", "netns", "add", nc.Netns}); err != nil {
		t.Fatal(err)
	}
	if err := m.checkpointCreatedNamespace(nc, j); err != nil {
		t.Fatal(err)
	}
	if err := m.runJournalIPSetup(t.Context(), nc, [][]string{{"ip", "link", "add", nc.VethHost, "type", "veth", "peer", "name", nc.VethPeer}}); err != nil {
		t.Fatal(err)
	}
	if err := m.run.Run(t.Context(), []string{"ip", "link", "set", nc.VethPeer, "netns", nc.Netns}); err != nil {
		t.Fatal(err)
	}
	if f.Phase != "spare" {
		claimed, err := m.alloc.adoptNetwork(l.Instance, idLive)
		if err != nil {
			t.Fatal(err)
		}
		if err := j.transferPrepared(l.Instance, idLive); err != nil {
			t.Fatal(err)
		}
		if f.Phase == "two_aliases" {
			newPath := filepath.Join("/run/netns", claimed.Netns)
			if err := os.WriteFile(newPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := unix.Mount(filepath.Join("/run/netns", nc.Netns), newPath, "", unix.MS_BIND, ""); err != nil {
				t.Fatal(err)
			}
		} else if f.Phase == "alias" || f.Phase == "guest" {
			if err := movePreparedNetns(nc.Netns, claimed.Netns); err != nil {
				t.Fatal(err)
			}
			if err := m.moveNamespaceObservation(nc.Netns, claimed.Netns); err != nil {
				t.Fatal(err)
			}
			old := nc.Instance
			nc.Instance, nc.Netns = claimed.Instance, claimed.Netns
			if err := m.transferNetworkLinks(old, nc); err != nil {
				t.Fatal(err)
			}
			if f.Phase == "guest" {
				if err := m.journalLease(journalTestLease(idLive, l.Slot)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := os.WriteFile(f.Ready, []byte(f.Phase), 0o600); err != nil {
		t.Fatal(err)
	}
	<-t.Context().Done() // parent SIGKILL must bypass all deferred cleanup
}
