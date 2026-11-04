// adr: 20261023115741 — the shared capability flag decider.
package capflags_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/productcap"
)

func flagKey(t *testing.T, capability string) string {
	t.Helper()
	key, ok := platformflags.CapabilityFlag(capability)
	if !ok {
		t.Fatalf("capability %q has no flag", capability)
	}
	return key
}

func load(t *testing.T, set *platformflags.Set) *capflags.Decider {
	t.Helper()
	snapshot := func() *platformflags.Snapshot { return nil }
	if set != nil {
		snapshot = set.Snapshot
	}
	d, err := capflags.Load(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// child returns a sub-feature and its parent, both with a generated flag.
func child(t *testing.T) (string, string) {
	t.Helper()
	catalog, err := productcap.Load()
	if err != nil {
		t.Fatal(err)
	}
	generated := map[string]bool{}
	for _, spec := range platformflags.Catalog() {
		if spec.Generated {
			generated[spec.Capability] = true
		}
	}
	for _, capability := range catalog.Capabilities {
		if capability.Parent != "" && generated[capability.ID] && generated[capability.Parent] {
			return capability.ID, capability.Parent
		}
	}
	t.Fatal("catalog has no sub-feature with generated flags")
	return "", ""
}

func TestTierDefaultsDecideWithoutAnOperatorValue(t *testing.T) {
	for name, set := range map[string]*platformflags.Set{
		"no snapshot":     nil,
		"catalog default": platformflags.NewSet(nil),
	} {
		t.Run(name, func(t *testing.T) {
			d := load(t, set)
			subject := platformflags.Subject{AccountID: "acct-1"}
			if !d.Enabled("github-deploys", subject) {
				t.Error("GA github-deploys must be on by default")
			}
			if d.Enabled("queues", subject) {
				t.Error("preview queues must be off by default")
			}
			if !d.Released("queues") {
				t.Error("a preview kill switch stays on, so allow rules can reveal it")
			}
			if !d.Enabled("scale-to-zero", subject) || !d.Enabled("not-a-capability", subject) {
				t.Error("always-on and unknown capabilities count as on")
			}
		})
	}
}

func TestAccountRuleRevealsAPreviewCapabilityForThatAccountOnly(t *testing.T) {
	on := true
	set := platformflags.NewSet(map[string]platformflags.Definition{
		flagKey(t, "queues"): {Enabled: true, Rules: []platformflags.Rule{{ID: "beta", Accounts: []string{"acct-1"}, Value: &on}}},
	})
	d := load(t, set)
	if !d.Enabled("queues", platformflags.Subject{AccountID: "acct-1"}) {
		t.Error("the allowed account must get queues")
	}
	if d.Enabled("queues", platformflags.Subject{AccountID: "acct-2"}) {
		t.Error("another account must not get queues")
	}
}

func TestSubFeatureFollowsItsParent(t *testing.T) {
	id, parent := child(t)
	subject := platformflags.Subject{AccountID: "acct-1"}
	set := platformflags.NewSet(map[string]platformflags.Definition{
		flagKey(t, id):     {Enabled: true, Default: true},
		flagKey(t, parent): {Enabled: true, Default: true},
	})
	d := load(t, set)
	if !d.Enabled(id, subject) || !d.Released(id) {
		t.Fatalf("%s with %s on must be on", id, parent)
	}

	set = platformflags.NewSet(map[string]platformflags.Definition{
		flagKey(t, id):     {Enabled: true, Default: true},
		flagKey(t, parent): {Enabled: false},
	})
	d = load(t, set)
	if d.Enabled(id, subject) || d.Released(id) {
		t.Fatalf("%s must be off while its parent %s is off", id, parent)
	}
}

func TestKillSwitchOffIsNotReleased(t *testing.T) {
	set := platformflags.NewSet(map[string]platformflags.Definition{flagKey(t, "github-deploys"): {Enabled: false, Default: true}})
	d := load(t, set)
	if d.Released("github-deploys") || d.Enabled("github-deploys", platformflags.Subject{AccountID: "acct-1"}) {
		t.Fatal("a capability with its kill switch off is off for everyone")
	}
}

func TestReleasedByDefaultIgnoresAccountRules(t *testing.T) {
	on := true
	id, parent := child(t)
	set := platformflags.NewSet(map[string]platformflags.Definition{
		flagKey(t, "queues"):         {Enabled: true, Rules: []platformflags.Rule{{ID: "beta", Accounts: []string{"acct-1"}, Value: &on}}},
		flagKey(t, "github-deploys"): {Enabled: true, Default: true},
		flagKey(t, id):               {Enabled: true, Default: true},
		flagKey(t, parent):           {Enabled: true, Default: false},
	})
	d := load(t, set)
	if d.ReleasedByDefault("queues") || !d.ReleasedByDefault("github-deploys") || d.ReleasedByDefault(id) {
		t.Fatal("released by default means kill switch and default on, for the capability and its ancestors")
	}
	if !load(t, nil).ReleasedByDefault("crons") || load(t, nil).ReleasedByDefault("realtime") {
		t.Fatal("without a snapshot, tier defaults decide")
	}
}

func TestNilDeciderAllowsEverything(t *testing.T) {
	var d *capflags.Decider
	if !d.Enabled("queues", platformflags.Subject{}) || !d.Released("queues") || d.Known("queues") {
		t.Fatal("a nil decider allows every capability and knows none")
	}
}

func TestBootstrapReleaseAll(t *testing.T) {
	if got := capflags.Bootstrap(func(string) string { return "" }); got != nil {
		t.Fatalf("Bootstrap without %s = %d flags, want none", capflags.ReleaseAllEnv, len(got))
	}
	seeded := capflags.Bootstrap(func(key string) string {
		if key == capflags.ReleaseAllEnv {
			return "1"
		}
		return ""
	})
	d := load(t, platformflags.NewSet(seeded))
	for _, capability := range []string{"queues", "github-deploys"} {
		if !d.Enabled(capability, platformflags.Subject{AccountID: "acct-1"}) {
			t.Errorf("%s must be on with %s=1", capability, capflags.ReleaseAllEnv)
		}
	}
	for _, spec := range platformflags.Catalog() {
		if _, ok := seeded[spec.Key]; ok && !spec.Generated {
			t.Errorf("release-all seeded hand-written flag %s; its own bootstrap decides", spec.Key)
		}
	}
}
