//go:build linux && metal

// adr: 404
// adr: 405
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

// The prior boot is injected journal provenance, not a real host reboot. Real
// foreign resources and UID holders must survive recovery unchanged.
func TestMetalResourceRestartPreparedPriorBoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root on dedicated Linux KVM")
	}
	for _, phase := range []string{"legacy_complete", "intent", "namespace_intent", "namespace_checkpoint", "link_intent", "complete", "retired"} {
		for _, collision := range []string{"absent", "namespace", "veth", "dummy", "uid"} {
			t.Run(phase+"/"+collision, func(t *testing.T) {
				path := t.TempDir()
				j := openTestResourceJournal(t, path)
				r := restartPreparedRecord(t, "prepared-"+uuid.NewString(), 0, uuid.NewString())
				if phase != "legacy_complete" {
					r = preparedBootRecord(t, r.Lease.Instance, r.Assets[0].Namespace.BootID, phase)
				}
				if err := j.beginRecord(r); err != nil {
					t.Fatal(err)
				}
				if err := j.Close(); err != nil {
					t.Fatal(err)
				}
				run := wire.ExecRunner{}
				var beforeNS *resourceAsset
				var beforeLink *resourceLinkIdentity
				var holder *exec.Cmd
				t.Cleanup(func() {
					if holder != nil && holder.Process != nil {
						_ = holder.Process.Kill()
						_ = holder.Wait()
					}
					ctx := context.WithoutCancel(t.Context())
					_ = run.Run(ctx, []string{"ip", "link", "del", r.Lease.VethHost})
					_ = run.Run(ctx, []string{"ip", "netns", "del", r.Lease.Netns})
					leakcheck.AssertZero(t)
				})
				switch collision {
				case "namespace":
					if err := run.Run(t.Context(), []string{"ip", "netns", "add", r.Lease.Netns}); err != nil {
						t.Fatal(err)
					}
					var err error
					beforeNS, err = resourceNetworkNamespaceAt(r.Lease.Netns)
					if err != nil || beforeNS == nil {
						t.Fatal("foreign namespace missing", err)
					}
				case "veth", "dummy":
					// A user-set address avoids asynchronous udev persistent-MAC
					// replacement of the kernel's randomly assigned veth address.
					args := []string{"ip", "link", "add", r.Lease.VethHost, "address", newResourceLinkAddress(), "type", collision}
					if collision == "veth" {
						args = append(args, "peer", "name", r.Lease.VethPeer)
					}
					if err := run.Run(t.Context(), args); err != nil {
						t.Fatal(err)
					}
					var err error
					beforeLink, err = resourceNetworkLinkAt(r.Lease.VethHost, 0)
					if err != nil || beforeLink == nil {
						t.Fatal("foreign link missing", err)
					}
				case "uid":
					holder = exec.CommandContext(t.Context(), "/usr/bin/sleep", "60")
					holder.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(r.Lease.UID), Gid: uint32(r.Lease.GID)}}
					if err := holder.Start(); err != nil {
						t.Fatal(err)
					}
				}
				j = openTestResourceJournal(t, path)
				observed := &fakeRunner{}
				m := newTestManager(observed, &fakeVMM{})
				if err := m.WithResourceJournal(j); err != nil {
					t.Fatal(err)
				}
				rep, err := m.RecoverRestartQuarantine(t.Context(), t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				wantReclaimed, wantSlot := 0, 1
				if collision == "absent" {
					wantReclaimed, wantSlot = 1, 0
				}
				if rep.ReclaimedPreparedRecords != wantReclaimed || rep.JournalRecords != 1-wantReclaimed || rep.Slots != 1-wantReclaimed {
					t.Fatalf("prior-boot recovery: %+v", rep)
				}
				l, err := m.alloc.reserveNetwork("fresh")
				if err != nil || l.Slot != wantSlot || len(observed.commands) != 0 || m.LeasedCount() != 0 {
					t.Fatal("reservation reused or recovery touched physical resources", l, err)
				}
				_, readErr := j.dir.ReadFile(resourceRecordName(r.Prepared.Source))
				if (collision == "absent" && !errors.Is(readErr, os.ErrNotExist)) || (collision != "absent" && readErr != nil) {
					t.Fatal("unexpected durable record state", readErr)
				}
				if beforeNS != nil {
					after, err := resourceNetworkNamespaceAt(r.Lease.Netns)
					if err != nil || after == nil || !namespaceCheckpointMatches(*beforeNS, *after) {
						t.Fatal("foreign namespace changed", err)
					}
				}
				if beforeLink != nil {
					after, err := resourceNetworkLinkAt(r.Lease.VethHost, beforeLink.Index)
					if err != nil || after == nil || *beforeLink != *after {
						t.Fatalf("foreign link changed: before=%+v after=%+v err=%v", beforeLink, after, err)
					}
				}
				if holder != nil {
					if err := holder.Process.Signal(syscall.Signal(0)); err != nil {
						t.Fatal("foreign UID holder signalled", err)
					}
					if _, err := os.Stat(filepath.Join("/proc", fmt.Sprint(holder.Process.Pid), "status")); err != nil {
						t.Fatal("foreign UID holder lost", err)
					}
				}
			})
		}
	}
}
