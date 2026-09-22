// spec: §11 — wake must publish changed host policy; repair remains forced (also §14).
package fcvm

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/netns"
)

type conditionalFakeHostRenderer struct {
	fakeHostRenderer
	conditionalCalls int
}

func (f *conditionalFakeHostRenderer) RenderIfChanged(context.Context) error {
	f.conditionalCalls++
	return nil
}

func TestHostSMTPWakeConditionalAndRepairForced(t *testing.T) {
	m := newTestManager(&fakeRunner{}, &fakeVMM{})
	r := &conditionalFakeHostRenderer{}
	m.SetHostRenderer(r)
	old := *netns.ActiveHostPolicyForRender()
	t.Cleanup(func() { netns.SwapActiveHostPolicy(old) })
	ctx := context.Background()
	m.renderHostSMTPAllowlistRules(ctx, true)
	if r.conditionalCalls != 1 || r.renderCalls != 0 {
		t.Fatal("wake did not select conditional renderer")
	}
	// Changing source-scoped exceptions must still publish the new policy
	// before asking the renderer whether its complete body changed.
	m.perAppAllowlist = map[string][]netip.Prefix{"app": {netip.MustParsePrefix("203.0.113.0/24")}}
	m.live["instance"] = &Instance{AppID: "app", Lease: Lease{HostIP: netip.MustParseAddr("10.100.0.2")}}
	m.renderHostSMTPAllowlistRules(ctx, true)
	if len(netns.ActiveHostPolicyForRender().SMTPAllowlistRules) != 1 {
		t.Fatal("wake lost SMTP exception")
	}
	m.live["instance"].Lease.HostIP = netip.MustParseAddr("10.100.0.3")
	m.renderHostSMTPAllowlistRules(ctx, true)
	if netns.ActiveHostPolicyForRender().SMTPAllowlistRules[0].SourceIP.String() != "10.100.0.3" {
		t.Fatal("wake retained stale source")
	}
	delete(m.live, "instance")
	m.rebuildHostSMTPAllowlistRules(ctx)
	if r.renderCalls != 1 || r.conditionalCalls != 3 {
		t.Fatal("teardown must force repair")
	}
	if len(netns.ActiveHostPolicyForRender().SMTPAllowlistRules) != 0 {
		t.Fatal("teardown retained exception")
	}
	// Legacy renderers preserve forced behavior even on wake.
	legacy := &fakeHostRenderer{}
	m.SetHostRenderer(legacy)
	m.renderHostSMTPAllowlistRules(ctx, true)
	if legacy.renderCalls != 1 {
		t.Fatal("legacy renderer was skipped")
	}
}
