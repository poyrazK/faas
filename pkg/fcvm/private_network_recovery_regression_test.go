package fcvm

// adr: 20261104120000

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type attachmentFailureRunner struct {
	fakeRunner
	failPolicy, failDelete bool
}

func (r *attachmentFailureRunner) Run(ctx context.Context, argv []string) error {
	if r.failPolicy && len(argv) == 6 && argv[4] == "nft" {
		return errors.New("injected nft transaction failure")
	}
	if r.failDelete && len(argv) == 4 && strings.Join(argv[:3], " ") == "ip link del" {
		return errors.New("injected unconfirmed link removal")
	}
	return r.fakeRunner.Run(ctx, argv)
}

func privateRecoveryFixture(t *testing.T, runner Runner) (*Manager, *state.MemStore, state.PrivateNetwork) {
	t.Helper()
	s := state.NewMemStore()
	n, err := s.CreatePrivateNetwork(t.Context(), state.PrivateNetwork{ID: "net-recovery", AccountID: "account", Name: "recovery", Region: "fra1", CIDR: netip.MustParsePrefix("10.92.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertAppPrivateNetworkAttachment(t.Context(), state.AppPrivateNetworkAttachment{AccountID: n.AccountID, AppID: "app", NetworkID: n.ID, Region: n.Region, Status: "ready", CIDRs: []netip.Prefix{n.CIDR}}); err != nil {
		t.Fatal(err)
	}
	m := newTestManager(runner, &fakeVMM{}).WithPrivateNetworkMembers(func(ctx context.Context, req PrivateNetworkMemberRequest) (PrivateNetworkMemberIdentity, error) {
		return s.EnsurePrivateNetworkMember(ctx, req.AccountID, req.AppID, req.NetworkID, req.InstanceID, req.Eligible)
	}, s.ReleasePrivateNetworkMember)
	return m, s, n
}

func wakePrivateRecoveryFixture(t *testing.T, m *Manager, id string, n *state.PrivateNetwork) *Instance {
	t.Helper()
	req := WakeRequest{Instance: id, AccountID: "account", AppID: "app", BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 1, MemSizeMiB: 128, Plan: api.PlanPro, EgressMbit: 10, Snapshot: usableSnapshot()}
	if n != nil {
		req.PrivateNetworkID, req.PrivateNetworkAddress, req.PrivateNetworkCIDRs = n.ID, "10.92.0.2", []string{n.CIDR.String()}
	}
	inst, err := m.Wake(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	return inst
}

func TestFailedLiveAttachmentClaimsRecover(t *testing.T) {
	for _, stage := range []string{"setup", "policy"} {
		for _, rollbackFails := range []bool{false, true} {
			for _, cleanup := range []string{"detach", "destroy"} {
				t.Run(stage+"/"+cleanup+"/rollback-fails="+map[bool]string{false: "false", true: "true"}[rollbackFails], func(t *testing.T) {
					r := &attachmentFailureRunner{}
					m, s, n := privateRecoveryFixture(t, r)
					inst := wakePrivateRecoveryFixture(t, m, "failed-attachment", nil)
					r.failDelete = rollbackFails
					if stage == "setup" {
						r.failOn = " master "
					} else {
						r.failPolicy = true
					}
					if err := m.UpdatePrivateNetworkAttachment(t.Context(), "app", n.ID, netip.MustParseAddr("10.92.0.2"), []netip.Prefix{n.CIDR}); err == nil {
						t.Fatal("attachment failure acknowledged")
					}
					if inst.Net.PrivateVethHost != "" {
						t.Fatal("failed attachment was published")
					}
					rows, err := s.ListPrivateNetworkAddresses(t.Context(), n.AccountID, n.ID)
					if err != nil {
						t.Fatal(err)
					}
					instances := 0
					for _, row := range rows {
						if row.OwnerType == "instance" {
							instances++
						}
					}
					if (instances != 0) != rollbackFails {
						t.Fatalf("claims released without confirmed cleanup, or leaked after rollback: %+v", rows)
					}
					// Match apid detach: release an unclaimed app reservation; an
					// active owner defers that release until runtime cleanup.
					if err := s.ReleasePrivateNetworkAddress(t.Context(), n.AccountID, n.ID, "app", "app"); err != nil && !errors.Is(err, state.ErrConflict) {
						t.Fatal(err)
					}
					if err := s.DeleteAppPrivateNetworkAttachment(t.Context(), n.AccountID, "app"); err != nil {
						t.Fatal(err)
					}
					r.failPolicy, r.failDelete, r.failOn = false, false, ""
					if cleanup == "detach" {
						if err := m.UpdatePrivateNetwork(t.Context(), "app", nil); err != nil {
							t.Fatal(err)
						}
					} else if err := m.Destroy(t.Context(), inst.Lease.Instance); err != nil {
						t.Fatal(err)
					}
					if rows, err := s.ListPrivateNetworkAddresses(t.Context(), n.AccountID, n.ID); err != nil || len(rows) != 0 {
						t.Fatalf("hidden attachment claims survived %s: %+v %v", cleanup, rows, err)
					}
					if err := s.DeletePrivateNetwork(t.Context(), n.AccountID, n.ID); err != nil {
						t.Fatalf("network still blocked by failed attachment: %v", err)
					}
				})
			}
		}
	}
}

func TestPrivateServicePromotionAnnouncementRetries(t *testing.T) {
	r := &attachmentFailureRunner{}
	m, s, n := privateRecoveryFixture(t, r)
	first := wakePrivateRecoveryFixture(t, m, "first-owner", &n)
	second := wakePrivateRecoveryFixture(t, m, "second-owner", &n)
	service := first.Net.PrivateNetworkServiceAddress
	if !r.ran(strings.Join(first.Net.PrivateNetworkARPNotifyCommand(), " ")) {
		t.Fatal("initial VIP ownership did not announce")
	}
	if err := m.Destroy(t.Context(), first.Lease.Instance); err != nil {
		t.Fatal(err)
	}
	r.failOn = " address "
	if err := m.ReconcilePrivateNetworkServices(t.Context()); err == nil {
		t.Fatal("unannounced promotion acknowledged")
	}
	if second.Net.PrivateNetworkServiceAddress.IsValid() {
		t.Fatal("failed announcement was cached as complete")
	}
	// The durable claim remains fenced while publication is retried.
	identity, err := s.EnsurePrivateNetworkMember(t.Context(), n.AccountID, "app", n.ID, "third", true)
	if err != nil || identity.Service.IsValid() {
		t.Fatalf("failed announcement permitted another owner: %+v %v", identity, err)
	}
	r.failOn = ""
	if err := m.ReconcilePrivateNetworkServices(t.Context()); err != nil {
		t.Fatal(err)
	}
	if second.Net.PrivateNetworkServiceAddress != service || !r.ran("ip netns exec "+second.Net.Netns+" ip link set dev "+second.Net.PrivateVethPeer+" address ") {
		t.Fatal("promotion did not retry its neighbor announcement")
	}
	before := len(r.commands)
	if err := m.ReconcilePrivateNetworkServices(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.commands) != before {
		t.Fatal("unchanged ownership repeated announcements")
	}
}

func TestPrivateReplacementRetainsWithdrawnDestinationHistory(t *testing.T) {
	r := &policyReplacementRunner{}
	m, _, n := privateRecoveryFixture(t, r)
	inst := wakePrivateRecoveryFixture(t, m, "history-owner", &n)
	if err := m.UpdatePrivateNetwork(t.Context(), "app", nil); err != nil {
		t.Fatal(err)
	}
	batch := string(r.input)
	if !strings.Contains(batch, "add element ip faas_private_policy destinations { 10.92.0.0/24 }") || !strings.Contains(batch, "faas_private_policy egress drop") || strings.Contains(batch, "delete table ip faas_private_policy") {
		t.Fatalf("replacement loses withdrawn-flow classification: %s", batch)
	}
	if len(inst.Net.PrivateNetworkCIDRs) != 0 {
		t.Fatal("detach not published")
	}
}

func TestPrivateServiceUnannouncedPromotionCanBeWithdrawn(t *testing.T) {
	for _, method := range []string{"reconcile", "attachment"} {
		t.Run(method, func(t *testing.T) {
			r := &attachmentFailureRunner{}
			m, s, n := privateRecoveryFixture(t, r)
			first := wakePrivateRecoveryFixture(t, m, "withdraw-old", &n)
			second := wakePrivateRecoveryFixture(t, m, "withdraw-pending", &n)
			service := first.Net.PrivateNetworkServiceAddress
			if err := m.Destroy(t.Context(), first.Lease.Instance); err != nil {
				t.Fatal(err)
			}
			r.failOn = " address "
			if err := m.ReconcilePrivateNetworkServices(t.Context()); err == nil {
				t.Fatal("announcement failure not injected")
			}
			r.failOn = ""
			// The deployment loses traffic before the pending announcement retries.
			m.privateNetworkMemberSource = func(ctx context.Context, req PrivateNetworkMemberRequest) (PrivateNetworkMemberIdentity, error) {
				return s.EnsurePrivateNetworkMember(ctx, req.AccountID, req.AppID, req.NetworkID, req.InstanceID, false)
			}
			var err error
			if method == "attachment" {
				err = m.UpdatePrivateNetworkAttachment(t.Context(), "app", n.ID, service, []netip.Prefix{n.CIDR})
			} else {
				err = m.ReconcilePrivateNetworkServices(t.Context())
			}
			if err != nil {
				t.Fatal(err)
			}
			if !r.ran("ip netns exec " + second.Net.Netns + " ip addr del " + service.String() + "/32 dev " + second.Net.PrivateVethPeer) {
				t.Fatal("unpublished bound VIP survived eligibility withdrawal")
			}
			identity, err := s.EnsurePrivateNetworkMember(t.Context(), n.AccountID, "app", n.ID, "withdraw-next", true)
			if err != nil || identity.Service != service {
				t.Fatalf("withdrawal did not release ownership after VIP removal: %+v %v", identity, err)
			}
		})
	}

}
