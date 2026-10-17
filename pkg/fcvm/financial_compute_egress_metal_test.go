//go:build linux && metal

package fcvm

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
)

// No forwarding or gateway is involved: this exercises the exact egress hook
// before the canonical root veth RX boundary, including raw minimum-size frames.
func TestMetalFinancialEgressPacketCredits(t *testing.T) {
	if os.Getenv("FAAS_FINANCIAL_COMPUTE_ACCEPTANCE") != "1" {
		t.Skip("opt-in financial native acceptance")
	}
	if os.Geteuid() != 0 {
		t.Fatal("financial packet acceptance requires root")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	suffix := uuid.NewString()[:8]
	nc := netns.Config{Netns: "fc-eg-" + suffix, VethHost: "vhe" + suffix, VethPeer: "vpe" + suffix, EgressBudgetBytes: 8192, EgressBudgetUntil: time.Now().Add(20 * time.Second)}
	run := func(argv ...string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
		if err != nil {
			t.Fatalf("packet fixture %v: %v: %s", argv, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("ip", "netns", "add", nc.Netns)
	t.Cleanup(func() {
		out, err := exec.Command("ip", "netns", "del", nc.Netns).CombinedOutput()
		if err != nil {
			t.Errorf("packet namespace cleanup: %v %s", err, out)
		}
		if out, err := exec.Command("ip", "link", "show", nc.VethHost).CombinedOutput(); err == nil {
			t.Errorf("packet veth leaked: %s", out)
		}
	})
	run("ip", "link", "add", nc.VethHost, "type", "veth", "peer", "name", nc.VethPeer)
	run("ip", "link", "set", nc.VethPeer, "netns", nc.Netns)
	for _, argv := range nc.EgressBudgetSetupCommands() {
		run(argv...)
	}
	run("ip", "link", "set", nc.VethHost, "up")
	run("ip", "netns", "exec", nc.Netns, "ip", "link", "set", nc.VethPeer, "up")
	burst := func() {
		run("ip", "netns", "exec", nc.Netns, "python3", "-c", financialRawEgressBurst, nc.VethPeer)
		time.Sleep(20 * time.Millisecond)
	}
	observed := func() uint64 {
		t.Helper()
		n, err := netns.ReadVethRXBytes(nc.VethHost)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := observed()
	burst()
	time.Sleep(20 * time.Millisecond)
	first := observed()
	if first <= before || first > uint64(nc.EgressBudgetBytes) {
		t.Fatalf("unfunded interface bytes: %d > %d", first, nc.EgressBudgetBytes)
	}
	burst()
	if now := observed(); now != first {
		t.Fatalf("exhausted quota reopened: %d -> %d", first, now)
	}
	// A new paid credit is the only operation that can reset the byte quota.
	for _, argv := range nc.EgressBudgetRefillCommands(nc.EgressBudgetBytes) {
		run(argv...)
	}
	burst()
	second := observed()
	if second <= first || second > uint64(2*nc.EgressBudgetBytes) {
		t.Fatalf("refill exceeded financed bytes: %d -> %d", first, second)
	}
	// Expiry blocks remaining credit independently of the VM process/daemon.
	nc.EgressBudgetUntil = time.Now().Add(-time.Second)
	for _, argv := range nc.EgressBudgetDeadlineCommands() {
		run(argv...)
	}
	for _, argv := range nc.EgressBudgetRefillCommands(nc.EgressBudgetBytes) {
		run(argv...)
	}
	burst()
	if now := observed(); now != second {
		t.Fatalf("expired UTC authority emitted %d bytes", now-second)
	}
	t.Logf("kernel interface bounds: first=%s total=%s financed=%d", strconv.FormatUint(first, 10), strconv.FormatUint(second, 10), 2*nc.EgressBudgetBytes)
}

const financialRawEgressBurst = `
import socket,struct,threading,sys
peer=sys.argv[1]
def send(index):
    s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW,socket.htons(3));s.bind((peer,0))
    eth=bytes.fromhex('ffffffffffff020000000001')
    packets=[eth+struct.pack('!H',0x0800)+bytes.fromhex('450000140000000040000000c6120002c6120001'),
             eth+struct.pack('!H',0x86dd)+bytes.fromhex('6000000000003b40')+bytes(32),
             eth+struct.pack('!H',0x0806)+bytes.fromhex('0001080006040001')+bytes(20),
             eth+struct.pack('!H',0x0800)+bytes.fromhex('450005dc0000000040110000c6120002c6120001')+bytes(1480),
             eth+struct.pack('!H',0x88b5)+bytes(40), eth+struct.pack('!H',0x0800)]
    for i in range(600):
        s.send(packets[(i+index)%len(packets)])
    s.close()
errors=[]
def checked_send(index):
    try: send(index)
    except Exception as error: errors.append(str(error))
threads=[threading.Thread(target=checked_send,args=(i,)) for i in range(8)]
for t in threads:t.start()
for t in threads:t.join()
if errors: raise SystemExit(errors)
`

func (f *financialNativeFixture) activateEgress() {
	f.t.Helper()
	f.activateJobAccount(1000000, 0)
	a, err := f.store.AccountByID(f.ctx, f.app.AccountID)
	if err != nil {
		f.t.Fatal(err)
	}
	for at := a.CreatedAt.Truncate(time.Minute); !at.After(time.Now().UTC().Truncate(time.Minute)); at = at.Add(time.Minute) {
		if err := f.store.RecordFinancialSamplingWindow(f.ctx, at, true, true); err != nil {
			f.t.Fatal(err)
		}
	}
	month := financial.ComputeMonth(time.Now())
	_, err = f.store.PutFinancialPriceSnapshot(f.ctx, state.FinancialPriceSnapshot{AccountID: a.ID, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0), EffectiveFrom: month, Plan: api.PlanHobby, DeliveryMode: "live", Price: financial.Price{Version: "native-account-egress", Meter: "egress", Currency: "EUR", Unit: "interface_bytes", MillicentsPerUnit: 1, UnitQuantity: 1024 * 1024, IncludedQuantity: api.FinancialEgressCreditBytes}})
	if err != nil {
		f.t.Fatal(err)
	}
	spec := f.policy.Spec
	spec.Meters = []string{"compute", "egress"}
	f.policy, err = f.store.UpdateFinancialBudget(f.ctx, a.ID, f.policy.ID, f.policy.Revision, "native-acceptance", spec)
	if err != nil {
		f.t.Fatal(err)
	}
}
func TestMetalFinancialEgressPostgresContention(t *testing.T) {
	f := newFinancialNativeFixture(t)
	f.configureJob(0)
	owners := []*financialNativeOwner{f.start(0, ""), f.start(1, "")}
	f.activateEgress()
	f.claimJob(0)
	var outcomes [2]financialNativeEvent
	var wg sync.WaitGroup
	for i, o := range owners {
		wg.Go(func() { outcomes[i] = o.call(t, financialNativeCommand{Operation: "wake"}) })
	}
	wg.Wait()
	winner := -1
	for i, out := range outcomes {
		if out.Error == "" {
			if winner >= 0 || out.Live != 1 {
				f.t.Fatalf("unfunded native start: %+v", outcomes)
			}
			winner = i
		} else if out.Error != "budget" || out.Live != 0 || out.Leased != 0 || out.Journal {
			t.Fatalf("byte refusal had physical effects: %+v", outcomes)
		}
	}
	if winner < 0 {
		t.Fatalf("no funded attempt: %+v", outcomes)
	}
	l := f.lease(owners[winner])
	if !l.EgressFunded || l.EgressGranted != api.FinancialEgressCreditBytes {
		t.Fatalf("guest has no byte funding: %+v", l)
	}
	owners[winner].must(t, "destroy")
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || b.Egress.ReservedBytes != 0 || b.Egress.CommittedBytes != l.EgressGranted || b.UnconfirmedLeases != 0 {
		t.Fatalf("native byte settlement: %+v %v", b, err)
	}
}
func TestMetalFinancialEgressPostgresRecovery(t *testing.T) {
	f := newFinancialNativeFixture(t)
	owner := f.start(0, "")
	f.start(1, "")
	f.activateEgress()
	owner.must(t, "wake")
	l := f.lease(owner)
	owner.must(t, "offline")
	owner.kill(t)
	b, err := f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || b.Egress.ReservedBytes < l.EgressGranted {
		t.Fatalf("dead owner released credit: %+v %v", b, err)
	}
	replacement := f.start(0, "")
	replacement.must(t, "settlement-off")
	e := replacement.call(t, financialNativeCommand{Operation: "recover"})
	if e.Error == "" || e.Leased == 0 {
		t.Fatalf("uncertain settlement released slot: %+v", e)
	}
	replacement.must(t, "settlement-on")
	replacement.must(t, "recover")
	b, err = f.store.FinancialComputeBalance(f.ctx, f.policy, time.Now())
	if err != nil || b.Egress.ReservedBytes != 0 || b.Egress.CommittedBytes < l.EgressGranted {
		t.Fatalf("recovery lost funded bytes: %+v %v", b, err)
	}
	if out := replacement.call(t, financialNativeCommand{Operation: "wake"}); out.Error != "fence" {
		t.Fatalf("old native attempt revived: %+v", out)
	}
}
