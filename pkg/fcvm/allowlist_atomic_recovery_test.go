// adr: 20261104170000
package fcvm

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type allowlistBatch struct{ namespace, script string }

// Model rule identity changes at transaction commit. A reference to an old
// deleted handle is rejected, as nft would reject it on a replay.
type allowlistTxnRunner struct {
	fakeRunner
	batches              []allowlistBatch
	handles              map[string]map[string]uint64
	next                 uint64
	failAttempt          int
	loseAck, failCapture bool
}

func (r *allowlistTxnRunner) apply(namespace, script string) error {
	r.batches = append(r.batches, allowlistBatch{namespace, script})
	if len(r.batches) == r.failAttempt {
		return errors.New("injected IPv6 transaction failure")
	}
	prior := r.handles[namespace]
	next := map[string]uint64{"ip": prior["ip"], "ip6": prior["ip6"]}
	for _, line := range strings.Split(script, "\n") {
		for _, command := range strings.Split(line, ";") {
			fields := strings.Fields(command)
			if len(fields) < 4 {
				continue
			}
			if fields[0] == "delete" && fields[1] == "table" && fields[3] == "faas" {
				next[fields[2]] = 0
				continue
			}
			if fields[1] != "rule" || fields[3] != "faas" {
				continue
			}
			family := fields[2]
			if fields[0] == "insert" || fields[0] == "delete" {
				for i, field := range fields {
					if field != "handle" || i+1 == len(fields) {
						continue
					}
					h, err := strconv.ParseUint(fields[i+1], 10, 64)
					if err != nil || h != prior[family] {
						return fmt.Errorf("stale %s handle %s", family, fields[i+1])
					}
				}
			}
			if fields[0] == "insert" || fields[0] == "add" && strings.Contains(command, "tcp dport != 25 accept") {
				r.next++
				next[family] = r.next
			} else if fields[0] == "delete" && next[family] == prior[family] {
				next[family] = 0
			}
		}
	}
	r.handles[namespace] = next
	if r.loseAck {
		r.loseAck = false
		return errors.New("lost acknowledgement after commit")
	}
	return nil
}

func (r *allowlistTxnRunner) RunInput(_ context.Context, argv []string, input []byte) error {
	return r.apply(argv[3], string(input))
}

func (r *allowlistTxnRunner) RunCapture(_ context.Context, argv []string) ([]byte, error) {
	if r.failCapture {
		r.failCapture = false
		return nil, errors.New("handle capture unavailable")
	}
	family := "ip"
	for _, field := range argv {
		if field == "ip6" {
			family = "ip6"
		}
	}
	h := r.handles[argv[3]][family]
	cidr := "203.0.113.0/25"
	if family == "ip6" {
		cidr = "2001:db8::/65"
	}
	return []byte(fmt.Sprintf("iifname \"tap0\" %s daddr { %s } tcp dport != 25 accept # handle %d\niifname \"tap0\" %s daddr . tcp dport @egress_db accept # handle 999\n", family, cidr, h, family)), nil
}

type allowlistCommandRunner struct{ next *allowlistTxnRunner }

func (r allowlistCommandRunner) Run(_ context.Context, argv []string) error {
	if len(argv) != 6 {
		return fmt.Errorf("expected one nft command program: %v", argv)
	}
	return r.next.apply(argv[3], argv[5])
}

func atomicAllowlistFixture(t *testing.T, stdin bool, replicas int) (*Manager, *allowlistTxnRunner, []netip.Prefix) {
	t.Helper()
	r := &allowlistTxnRunner{handles: map[string]map[string]uint64{}, next: 1000}
	var runner Runner = r
	if !stdin {
		runner = allowlistCommandRunner{r}
	}
	m := newTestManager(runner, &fakeVMM{}).WithCaptureRunner(r)
	for i := 0; i < replicas; i++ {
		id := fmt.Sprintf("atomic-%d", i)
		seedPolicyInstance(m, id, "atomic-app", api.PlanPro)
		inst := m.live[id]
		inst.Net.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("2001:db8::/64")}
		inst.AllowlistHandleV4, inst.AllowlistHandleV6 = 100, 200
		r.handles[inst.Net.Netns] = map[string]uint64{"ip": 100, "ip6": 200}
	}
	return m, r, []netip.Prefix{netip.MustParsePrefix("203.0.113.0/25"), netip.MustParsePrefix("2001:db8::/65")}
}

func TestAllowlistFamiliesCommitAtomicallyAndRetry(t *testing.T) {
	for _, stdin := range []bool{false, true} {
		t.Run(fmt.Sprint("stdin=", stdin), func(t *testing.T) {
			m, r, wanted := atomicAllowlistFixture(t, stdin, 1)
			r.failAttempt = 1
			if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err == nil {
				t.Fatal("failed transaction acknowledged")
			}
			inst := m.live["atomic-0"]
			if len(r.batches) != 1 || r.handles[inst.Net.Netns]["ip"] != 100 || r.handles[inst.Net.Netns]["ip6"] != 200 {
				t.Fatal("failed transaction changed one family")
			}
			for _, want := range []string{"insert rule ip faas forward handle 100", "insert rule ip6 faas forward handle 200"} {
				if !strings.Contains(r.batches[0].script, want) {
					t.Fatal("families were not in the same transaction", r.batches)
				}
			}
			if inst.AllowlistHandleV4 != 0 || inst.AllowlistHandleV6 != 0 {
				t.Fatal("failed transaction left trusted handles")
			}
			r.failAttempt = 0
			if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err != nil {
				t.Fatal("retry did not converge", err)
			}
			if !samePrefixSet(inst.Net.EgressAllowlist, wanted) || len(r.batches) != 2 || !strings.Contains(r.batches[1].script, "delete table ip faas") {
				t.Fatal("retry did not reconstruct uncertain policy")
			}
		})
	}
}

func TestAllowlistRetainsCompletedReplicaWhenAnotherFails(t *testing.T) {
	m, r, wanted := atomicAllowlistFixture(t, true, 2)
	r.failAttempt = 2
	if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err == nil {
		t.Fatal("replica failure acknowledged")
	}
	var complete, pending *Instance
	for _, inst := range m.live {
		if samePrefixSet(inst.Net.EgressAllowlist, wanted) {
			complete = inst
		} else {
			pending = inst
		}
	}
	if complete == nil || pending == nil || complete.AllowlistHandleV4 != r.handles[complete.Net.Netns]["ip"] {
		t.Fatal("successful replica progress was lost")
	}
	r.failAttempt = 0
	if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err != nil {
		t.Fatal(err)
	}
	if len(r.batches) != 3 || r.batches[2].namespace != pending.Net.Netns {
		t.Fatal("retry revisited completed replica or missed pending replica")
	}
}

func TestAllowlistRecoversFromUncertainCommitOrCapture(t *testing.T) {
	for _, fault := range []string{"lost-ack", "capture"} {
		t.Run(fault, func(t *testing.T) {
			m, r, wanted := atomicAllowlistFixture(t, true, 1)
			r.loseAck = fault == "lost-ack"
			r.failCapture = fault == "capture"
			if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err == nil {
				t.Fatal("uncertain mutation acknowledged")
			}
			inst := m.live["atomic-0"]
			if inst.AllowlistHandleV4 != 0 || inst.AllowlistHandleV6 != 0 {
				t.Fatal("uncertain handles remained trusted")
			}
			if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err != nil {
				t.Fatal("retry reused deleted handles", err)
			}
			if !samePrefixSet(inst.Net.EgressAllowlist, wanted) {
				t.Fatal("retry did not publish completed policy")
			}
		})
	}
}

func TestAllowlistHandleCaptureExcludesOtherDestinationRules(t *testing.T) {
	for _, family := range []string{"ip", "ip6"} {
		for _, duplicate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/duplicate=%v", family, duplicate), func(t *testing.T) {
				public := fmt.Sprintf("iifname \"tap0\" %s daddr { 203.0.113.0/25 } tcp dport != 25 accept # handle 42\n", family)
				output := fmt.Sprintf("iifname \"tap0\" %s daddr { 10.42.0.0/24 } accept # handle 9\n", family) + public
				output += fmt.Sprintf("iifname \"tap0\" %s daddr . tcp dport @egress_db accept # handle 99\n", family)
				if duplicate {
					output += public
				}
				h, err := listChainHandles(t.Context(), &fakeCaptureRunner{listChainOutput: []byte(output)}, "fc-owner", family, "faas", "forward")
				want := uint64(42)
				if duplicate {
					want = 0
				}
				if err != nil || h != want {
					t.Fatalf("handle=%d err=%v want=%d", h, err, want)
				}
			})
		}
	}
}

func TestAllowlistRebuildsUnknownHandlesEvenWhenPrefixesMatch(t *testing.T) {
	m, r, wanted := atomicAllowlistFixture(t, true, 1)
	inst := m.live["atomic-0"]
	inst.Net.EgressAllowlist = append([]netip.Prefix{}, wanted...)
	inst.AllowlistHandleV4, inst.AllowlistHandleV6 = 0, 0
	// The old kernel rule may still be broader after a legacy handle mix-up.
	// Matching the desired cached prefixes cannot establish convergence.
	r.loseAck = true
	if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err == nil {
		t.Fatal("uncertain full replacement acknowledged")
	}
	if inst.AllowlistHandleV4 != 0 || inst.AllowlistHandleV6 != 0 {
		t.Fatal("replacement retained uncertain handles")
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err != nil {
		t.Fatal("full replacement retry failed", err)
	}
	if len(r.batches) != 2 || !strings.Contains(r.batches[1].script, "delete table ip faas") {
		t.Fatal("same-prefix retry did not rebuild unknown policy")
	}
}

func TestAllowlistRecoveryPreservesTimedExceptions(t *testing.T) {
	m, r, wanted := atomicAllowlistFixture(t, true, 1)
	inst := m.live["atomic-0"]
	inst.Net.DNSGated, inst.Net.DatabaseEgress = true, true
	until := time.Now().Add(30 * time.Second)
	soon := time.Now().Add(500 * time.Millisecond)
	addr, short := netip.MustParseAddr("198.51.100.10"), netip.MustParseAddr("198.51.100.11")
	inst.resolvedEgress = map[netip.Addr]time.Time{addr: until, short: soon}
	inst.databaseEgress = map[databaseEgressKey]time.Time{{addr: addr, port: 5432}: until, {addr: short, port: 5432}: soon}
	r.loseAck = true
	if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err == nil {
		t.Fatal("lost acknowledgement accepted")
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), "atomic-app", 1, wanted, nil); err != nil {
		t.Fatal(err)
	}
	script := r.batches[1].script
	for _, want := range []string{"egress_resolved { 198.51.100.10 timeout ", "egress_db { 198.51.100.10 . 5432 timeout "} {
		if !strings.Contains(script, want) {
			t.Fatal("rebuild lost active timed exception", want)
		}
	}
	if strings.Contains(script, short.String()) || !inst.resolvedEgress[addr].Equal(until) || !inst.databaseEgress[databaseEgressKey{addr: addr, port: 5432}].Equal(until) {
		t.Fatal("rebuild renewed an expiry or restored a subsecond grant")
	}
	for _, line := range strings.Split(script, "\n") {
		if !strings.Contains(line, addr.String()) || !strings.Contains(line, "timeout ") {
			continue
		}
		fields := strings.Fields(line)
		for i, field := range fields {
			if field == "timeout" && i+1 < len(fields) {
				ttl, err := time.ParseDuration(fields[i+1])
				if err != nil || ttl <= 0 || ttl > 30*time.Second {
					t.Fatalf("exception lifetime extended: %s", line)
				}
			}
		}
	}
}
