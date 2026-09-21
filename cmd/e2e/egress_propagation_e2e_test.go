// egress_propagation_e2e_test.go — does a committed egress policy reach the node?
//
// Tenant egress is a §11 ship-blocking control: deny 25/465/587, deny RFC1918,
// link-local and metadata ranges, plus the per-app allowlist on top. The
// enforcement is nftables inside the guest's netns and needs metal to verify.
// Whether the policy ever ARRIVES is a different question, it is pure control
// plane, and it had no coverage at all.
//
// The path (ADR-031/033):
//
//	apid writes apps.egress_allowlist
//	  -> pg_notify app_changed{kind:"updated"}
//	    -> schedd's EgressDriftSubscriber re-reads the column
//	      -> pushes to every vmmd owning a live instance, deduped per node
//
// Nothing in CI could reach it: FakeVMMD left UpdateEgressAllowlist
// Unimplemented, so any test that triggered the fan-out got an error back and
// no test triggered it.
//
// The failure this catches is the quiet kind. A tenant tightens their egress
// policy, apid returns 200, the row is committed — and the running VM keeps
// the old rules because the notification was dropped, the subscriber was not
// wired, or the fan-out skipped the node. Nothing errors, the API says yes,
// and the guest is still talking to whatever it was talking to before. That is
// the shape of the two production incidents where a subscriber was never bound
// (PR #1286, PR #1287): committed state that never became running state.

package e2e_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestE2E_Egress_AllowlistReachesTheNodeHostingTheApp changes the allowlist on
// a live app and requires the change to arrive at the node actually running it.
func TestE2E_Egress_AllowlistReachesTheNodeHostingTheApp(t *testing.T) {
	f := newNormalPathFixture(t, "egress-propagation")
	if f == nil {
		return
	}

	// A live instance is the precondition: the subscriber fans out to nodes
	// hosting live instances, so an app with none has nowhere to send to and
	// the test would pass vacuously.
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)
	if _, err := f.store.RunningInstanceForApp(f.ctx, f.app.ID); err != nil {
		t.Fatalf("no live instance to fan out to: %v", err)
	}

	before := len(f.vmmd.EgressUpdates())

	// Hobby allows an allowlist of up to 8 entries. The /0 forms are rejected
	// by the apps_egress_allowlist_cidr trigger, so use real prefixes.
	allowlist := []string{"203.0.113.0/24", "198.51.100.7/32"}
	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
		api.UpdateAppRequest{EgressAllowlist: &allowlist})
	if status != http.StatusOK {
		t.Fatalf("patch egress allowlist: status=%d body=%s", status, body)
	}

	// The API returning 200 means the row is committed. It says nothing about
	// the guest, which is the entire gap this test exists for.
	var got []string
	waitForWake(t, 30*time.Second, func() bool {
		updates := f.vmmd.EgressUpdates()
		if len(updates) <= before {
			return false
		}
		last := updates[len(updates)-1]
		if last.GetAppId() != f.app.ID {
			return false
		}
		got = last.GetEgressAllowlist()
		return len(got) > 0
	}, "apid committed an egress allowlist and no vmmd ever received it. The app has a live "+
		"instance, so the fan-out had somewhere to go: either app_changed was not delivered, "+
		"the EgressDriftSubscriber is not wired, or it skipped the node. The API answered 200 "+
		"and the guest is still running the old rules")

	// The prefixes must arrive intact. A fan-out that fires with the wrong
	// payload is worse than one that does not fire: the node applies a policy
	// the tenant never asked for, and it looks like it worked.
	if len(got) != len(allowlist) {
		t.Fatalf("node received %d prefixes %v, want %d %v", len(got), got, len(allowlist), allowlist)
	}
	want := map[string]bool{}
	for _, p := range allowlist {
		want[p] = true
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("node received prefix %q which was never in the committed allowlist %v", p, allowlist)
		}
	}
}

// TestE2E_Egress_ClearingTheAllowlistAlsoPropagates covers the direction that
// matters more for security.
//
// Adding a prefix that never arrives is a broken feature. REMOVING one that
// never arrives is an open hole: the tenant revoked access, the API agreed,
// and the guest can still reach the address. A fan-out that only fires on
// growth would pass the test above and leave that hole wide open.
func TestE2E_Egress_ClearingTheAllowlistAlsoPropagates(t *testing.T) {
	f := newNormalPathFixture(t, "egress-revoke")
	if f == nil {
		return
	}
	dep := createNormalPathParkedDeployment(t, f)
	seedNormalPathSnapshot(t, f, dep.ID, normalPathSnapshotOpts{})
	wakeNormalPathApp(t, f)

	granted := []string{"203.0.113.0/24"}
	if _, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
		api.UpdateAppRequest{EgressAllowlist: &granted}); status != http.StatusOK {
		t.Fatalf("grant: status=%d", status)
	}
	waitForWake(t, 30*time.Second, func() bool {
		for _, u := range f.vmmd.EgressUpdates() {
			if u.GetAppId() == f.app.ID && len(u.GetEgressAllowlist()) == 1 {
				return true
			}
		}
		return false
	}, "the granted allowlist never reached the node; the revoke below would prove nothing")

	afterGrant := len(f.vmmd.EgressUpdates())

	// Revoke everything.
	revoked := []string{}
	if _, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug,
		api.UpdateAppRequest{EgressAllowlist: &revoked}); status != http.StatusOK {
		t.Fatalf("revoke: status=%d", status)
	}

	waitForWake(t, 30*time.Second, func() bool {
		updates := f.vmmd.EgressUpdates()
		if len(updates) <= afterGrant {
			return false
		}
		last := updates[len(updates)-1]
		return last.GetAppId() == f.app.ID && len(last.GetEgressAllowlist()) == 0
	}, "revoking the allowlist never reached the node. The tenant removed an address, the API "+
		"returned 200, and the guest can still reach it — a fan-out that only fires when the "+
		"list grows leaves every revocation silently unapplied")
}
