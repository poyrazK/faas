// adr: 477
// adr: 479
package fcvm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func preparedJournalFixture(t *testing.T) (*Manager, *preparedNetworkPool, *ResourceJournal, *namespaceFixture) {
	t.Helper()
	m, p := testPreparedPool(t, 1)
	m.preparedNetworks = nil
	j := openTestResourceJournal(t, t.TempDir())
	if err := m.WithResourceJournal(j); err != nil {
		t.Fatal(err)
	}
	m.preparedNetworks = p
	f := installNamespaceFixture(m)
	p.move = func(old, next string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		a := cloneResourceAssets([]resourceAsset{f.bindings[old]})[0]
		a.Path, a.Mount.MountID = filepath.Join("/run/netns", next), a.Mount.MountID+100
		f.bindings[next] = a
		delete(f.bindings, old)
		return nil
	}
	return m, p, j, f
}

func TestResourcePreparedOrderingAndGuestLifecycle(t *testing.T) {
	for _, port := range []int{8080, 3000} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			m, p, j, f := preparedJournalFixture(t)
			f.before = func(argv []string) {
				if len(argv) >= 3 && argv[0] == "ip" && argv[2] == "add" {
					records, err := j.snapshot()
					if err != nil || len(records) != 1 || !records[0].preparedSpare() || records[0].Version != 6 || records[0].Prepared.BootID != idLive || len(records[0].Assets) == 0 {
						t.Fatal("physical creation preceded durable spare/asset intent")
					}
				}
			}
			fillTestPreparedPool(t, m, p, 100)
			if len(p.ready) != 1 || m.LeasedCount() != 0 {
				t.Fatal("spare missing or charged as VM admission")
			}
			spare := p.ready[0]
			move := p.move
			p.move = func(old, next string) error {
				r, ok, err := j.lookup(idLive)
				if err != nil || !ok || !r.preparedSpare() || r.Prepared.Target != idLive || r.Lease.Instance != spare.lease.Instance {
					t.Fatal("alias move preceded transfer intent")
				}
				return move(old, next)
			}
			f.before = nil
			req := wakeReq(idLive, nil)
			req.Plan, req.EgressMbit, req.Port = api.PlanScale, 100, port
			inst, err := m.Wake(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			r, ok, err := j.lookup(idLive)
			if err != nil || !ok || r.Version != 6 || r.preparedSpare() || r.Prepared.BootID != idLive || r.Prepared.Source != spare.lease.Instance || r.Lease != inst.Lease || len(r.Assets) != 2 {
				t.Fatalf("guest intent/checkpoints: %+v, %v", r, err)
			}
			if _, err := j.dir.ReadFile(resourceRecordName(spare.lease.Instance)); err != nil {
				t.Fatal("stable spare record disappeared at adoption")
			}
			if _, err := j.dir.ReadFile(resourceRecordName(idLive)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("handoff published a second slot record")
			}
			if err := m.Destroy(t.Context(), idLive); err != nil {
				t.Fatal(err)
			}
			records, err := j.snapshot()
			if err != nil || len(records) != 0 || m.LeasedCount() != 0 || len(f.bindings)+len(f.links) != 0 {
				t.Fatal("guest teardown failed to retire stable record and resources")
			}
		})
	}
}

func TestResourcePreparedRestartQuarantine(t *testing.T) {
	for _, phase := range []string{"intent", "spare", "before_move", "after_move", "guest"} {
		t.Run(phase, func(t *testing.T) {
			m, p, j, _ := preparedJournalFixture(t)
			var source string
			if phase == "intent" {
				l := leaseForSlot("prepared-"+idOther, 0)
				source = l.Instance
				if err := j.beginPrepared(l, idLive); err != nil {
					t.Fatal(err)
				}
			} else {
				policy := fillTestPreparedPool(t, m, p, 100)
				source = p.ready[0].lease.Instance
				if phase == "before_move" {
					if err := j.transferPrepared(source, idLive); err != nil {
						t.Fatal(err)
					}
				} else if phase == "after_move" || phase == "guest" {
					e := p.claim(idLive, policy)
					if e == nil {
						t.Fatal("claim failed")
					}
					if phase == "guest" {
						if err := m.journalLease(journalTestLease(idLive, e.lease.Slot)); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			// Simulate lost live observations; the durable record alone must
			// quarantine even when inventory finds no physical resources.
			p.ready, p.retired = nil, nil
			path := j.dir.Name()
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			j = openTestResourceJournal(t, path)
			fresh := newTestManager(&fakeRunner{}, &fakeVMM{})
			if err := fresh.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			rep, err := fresh.recoverRestartQuarantine(t.Context(), restartFixture(t))
			wantIDs := 1
			if phase == "before_move" || phase == "after_move" || phase == "guest" {
				wantIDs = 2
			}
			if err != nil || rep.JournalRecords != 1 || rep.Instances != wantIDs || rep.Slots != 1 || fresh.LeasedCount() != 0 {
				t.Fatalf("restart inventory: %+v, %v", rep, err)
			}
			for _, id := range []string{source, idLive}[:wantIDs] {
				if fresh.HasInstanceOwnership(id) || !errors.Is(fresh.Destroy(t.Context(), id), ErrRestartQuarantine) {
					t.Fatal("durable intent granted cleanup authority")
				}
				if _, err := fresh.Wake(t.Context(), WakeRequest{Instance: id}); !errors.Is(err, ErrRestartQuarantine) {
					t.Fatal("prepared identity admitted after restart")
				}
			}
			l, err := fresh.alloc.reserveNetwork("next-spare")
			if err != nil || l.Slot != 1 {
				t.Fatal("restart reused prepared slot")
			}
		})
	}
}

func TestResourcePreparedCommitFailuresRetainAndRetry(t *testing.T) {
	for _, phase := range []string{"intent", "transfer", "adoption", "retirement", "transfer_before_rename"} {
		t.Run(phase, func(t *testing.T) {
			m, p, j, f := preparedJournalFixture(t)
			syncDir := j.directorySync
			injected := errors.New("prepared directory fsync unavailable")
			if phase == "intent" {
				j.directorySync = func() error { return injected }
				fillTestPreparedPool(t, m, p, 100)
				if len(f.bindings)+len(f.links) != 0 || len(p.ready) != 0 || len(p.retired) != 1 || len(m.alloc.reserved) != 1 {
					t.Fatal("failed spare commit created resources or returned slot")
				}
			} else {
				policy := fillTestPreparedPool(t, m, p, 100)
				if phase == "transfer_before_rename" {
					// A competing target record rejects transfer before persistence.
					if err := j.begin(journalTestLease(idLive, 1)); err != nil {
						t.Fatal(err)
					}
					if p.claim(idLive, policy) != nil || len(p.retired) != 0 || len(m.alloc.reserved) != 0 || m.LeasedCount() != 0 {
						t.Fatal("unmoved spare retirement retained or released wrong owner")
					}
					records, _ := j.snapshot()
					if len(records) != 1 || records[0].Lease.Instance != idLive {
						t.Fatal("failed transfer leaked source intent or removed competing target")
					}
					return
				}
				if phase == "transfer" {
					j.directorySync = func() error { return injected }
					p.move = func(string, string) error { t.Fatal("namespace moved after failed transfer commit"); return nil }
					if p.claim(idLive, policy) != nil || len(p.retired) != 1 || m.LeasedCount() != 1 {
						t.Fatal("failed transfer released adopted slot")
					}
				} else {
					e := p.claim(idLive, policy)
					if e == nil {
						t.Fatal("claim failed")
					}
					j.directorySync = func() error { return injected }
					if phase == "adoption" {
						if err := m.journalLease(journalTestLease(idLive, e.lease.Slot)); !errors.Is(err, injected) {
							t.Fatalf("adoption commit: %v", err)
						}
					}
					p.discard(*e)
					if len(p.retired) != 1 || m.LeasedCount() != 1 {
						t.Fatal("uncertain retirement released slot")
					}
				}
			}
			j.directorySync = syncDir
			retired := p.retired
			p.retired = nil
			for _, e := range retired {
				p.discard(e)
			}
			records, err := j.snapshot()
			if err != nil || len(records)+len(p.retired)+len(m.alloc.reserved)+m.LeasedCount()+len(f.bindings)+len(f.links) != 0 {
				t.Fatal("confirmed retry retained resources or slot")
			}
		})
	}
}

func TestResourcePreparedEarlyWakeFailureAndNetworkless(t *testing.T) {
	for _, networkless := range []bool{false, true} {
		t.Run(fmt.Sprint(networkless), func(t *testing.T) {
			m, p, j, _ := preparedJournalFixture(t)
			fillTestPreparedPool(t, m, p, 100)
			req := wakeReq(idLive, nil)
			req.Plan, req.EgressMbit, req.ExecutionOnly = api.PlanScale, 100, networkless
			req.ExecutionMode = "invalid"
			if _, err := m.Wake(t.Context(), req); err == nil {
				t.Fatal("invalid wake accepted")
			}
			records, err := j.snapshot()
			want := 0
			if networkless {
				want = 1
			}
			if err != nil || len(records) != want || len(p.ready) != want || m.LeasedCount() != 0 {
				t.Fatal("early wake leaked handoff or consumed spare for networkless request")
			}
		})
	}
}

func TestResourcePreparedRejectsMalformedRecordsAndLaunch(t *testing.T) {
	for _, change := range []string{"version", "source", "target", "plan", "process", "asset", "storage_key", "overlap"} {
		t.Run(change, func(t *testing.T) {
			path := t.TempDir()
			j := openTestResourceJournal(t, path)
			l := leaseForSlot("prepared-"+idOther, 0)
			if err := j.beginPrepared(l, idLive); err != nil {
				t.Fatal(err)
			}
			v := NewJailerVMM(t.TempDir(), 0)
			v.SetResourceJournal(j)
			if err := v.prepareJournalLaunch(l); err == nil {
				t.Fatal("spare authorized guest launch")
			}
			r, _, _ := j.lookup(l.Instance)
			name := resourceRecordName(l.Instance)
			switch change {
			case "version":
				r.Version = 4
			case "source":
				r.Prepared.Source = "prepared-not-a-uuid"
			case "target":
				r.Prepared.Target = "../guest"
			case "plan":
				r.Lease.Plan = api.PlanPro
			case "process":
				r.Process = &resourceProcessIdentity{PID: 100, BootID: idLive, StartTicks: 1}
			case "asset":
				r.Assets = []resourceAsset{{Kind: "clone", Path: "/tmp/clone"}}
			case "storage_key":
				if err := j.dir.Remove(name); err != nil {
					t.Fatal(err)
				}
				name = resourceRecordName(idLive)
			case "overlap":
				r.Prepared.Target = idLive
				if err := j.begin(journalTestLease(idLive, 1)); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := j.dir.WriteFile(name, body, 0o600); err != nil {
				t.Fatal(err)
			}
			_ = j.Close()
			fresh, err := OpenResourceJournal(path)
			if fresh != nil {
				_ = fresh.Close()
			}
			if err == nil {
				t.Fatal("malformed/overlapping prepared record opened")
			}
		})
	}
}

func TestResourcePreparedSnapshotsDoNotMutateIdentity(t *testing.T) {
	j := openTestResourceJournal(t, t.TempDir())
	l := leaseForSlot("prepared-"+idOther, 0)
	if err := j.beginPrepared(l, idLive); err != nil {
		t.Fatal(err)
	}
	r, _, _ := j.lookup(l.Instance)
	r.Prepared.Source, r.Prepared.BootID = "changed", idDead
	items, _ := j.snapshot()
	items[0].Prepared.Target = idLive
	r, _, _ = j.lookup(l.Instance)
	if r.Prepared.Source != l.Instance || r.Prepared.Target != "" || r.Prepared.BootID != idLive {
		t.Fatal("snapshot mutated prepared identity")
	}
}
