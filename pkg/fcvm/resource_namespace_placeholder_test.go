// adr: 475
package fcvm

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

// A wake cancelled while `ip netns add` ran left /run/netns/<name> as an
// empty, unmounted marker. Every later probe failed with "network namespace
// marker is not nsfs", so the job VM's cleanup never finished and vmmd
// retained its lease. The manager's own unfinished creation is now released;
// a binding it checkpointed is still never destroyed on a mismatch.
func TestInterruptedNamespaceCreationDoesNotBlockTeardown(t *testing.T) {
	for _, tc := range []struct {
		name         string
		checkpointed bool
		wantTeardown bool
	}{
		{name: "creation interrupted before the mount", wantTeardown: true},
		{name: "checkpointed namespace replaced by a placeholder", checkpointed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := openTestResourceJournal(t, t.TempDir())
			m := newTestManager(&fakeRunner{}, &fakeVMM{})
			if err := m.WithResourceJournal(j); err != nil {
				t.Fatal(err)
			}
			f := installNamespaceFixture(m)
			fixtureProbe := m.namespaceProbe
			var placeholder atomic.Bool
			m.namespaceProbe = func(name string) (*resourceAsset, error) {
				if placeholder.Load() {
					return nil, errNamespacePlaceholder
				}
				return fixtureProbe(name)
			}
			if !tc.checkpointed {
				// `ip netns add` stops after creating the marker; `ip
				// netns del` unlinks it as the real tool does.
				f.before = func(argv []string) {
					if len(argv) == 4 && argv[1] == "netns" {
						placeholder.Store(argv[2] == "add")
					}
				}
			}
			l := journalTestLease(idLive, 0)
			if err := j.begin(l); err != nil {
				t.Fatal(err)
			}
			nc := netns.NewConfig(l.Instance, l.Netns, l.VethHost, l.VethPeer, l.HostIP)
			err := m.setupNetwork(t.Context(), nc)
			if tc.checkpointed {
				if err != nil {
					t.Fatal(err)
				}
				placeholder.Store(true)
			} else if !errors.Is(err, errNamespacePlaceholder) {
				t.Fatalf("setup over a placeholder = %v", err)
			}
			if !tc.wantTeardown {
				if err := m.checkOwnedNamespace(nc.Netns); !errors.Is(err, errNamespacePlaceholder) {
					t.Fatalf("a checkpointed namespace's replacement was accepted: %v", err)
				}
				return
			}
			if err := m.removeNamespaceForRebuild(t.Context(), nc); err != nil {
				t.Fatalf("teardown stuck on an interrupted creation's marker: %v", err)
			}
		})
	}
}
