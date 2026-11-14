// adr: 20261104160000
package gateway

import (
	"context"
	"errors"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestServiceDiscoveryDNSCapturesOwnerBeforeUpstream(t *testing.T) {
	for _, scenario := range []string{"address-reused", "capture-failed", "blocked-alias"} {
		t.Run(scenario, func(t *testing.T) {
			var prepared, granted atomic.Int32
			var owner atomic.Value
			owner.Store("original-owner")
			upstream := startServiceDNSFixture(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
				if prepared.Load() != 1 {
					t.Error("upstream query preceded owner capture")
				}
				owner.Store("replacement-owner")
				resp := new(dns.Msg)
				resp.SetReply(req)
				resp.Answer = []dns.RR{serviceDNSAddress(req.Question[0].Name, "198.51.100.10")}
				if scenario == "blocked-alias" {
					resp.Answer = append(resp.Answer, serviceDNSAlias(req.Question[0].Name, "pool.supportxmr.com."))
				}
				_ = w.WriteMsg(resp)
			}))
			h, err := NewServiceDiscoveryDNSHandler(netip.MustParseAddr("10.100.0.1"), []string{upstream}, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			h.WithBlocklist(NewDNSBlocklist(nil), nil)
			h.WithResolvedEgressHook(func(context.Context, string, string, []netip.Addr, time.Duration) error {
				t.Error("unfenced fallback hook ran")
				return nil
			})
			h.WithResolvedEgressPreparation(func(ctx context.Context, remote, name string) (ResolvedEgressHook, error) {
				prepared.Add(1)
				original := owner.Load().(string)
				if _, ok := ctx.Deadline(); !ok || name != "allowed.example" || remote == "" {
					t.Error("capture lost query identity or deadline")
				}
				if scenario == "capture-failed" {
					return nil, errors.New("owner unavailable")
				}
				return func(finishCtx context.Context, finishRemote, finishName string, addrs []netip.Addr, _ time.Duration) error {
					granted.Add(1)
					if finishCtx != ctx || original != "original-owner" || finishRemote != remote || finishName != name || len(addrs) != 1 {
						t.Error("completed answer changed its original owner or context")
					}
					return nil
				}, nil
			})
			resp := queryServiceDNSFixture(t, "udp", startServiceDNSFixture(t, h), 0)
			wantGrants, wantRcode := int32(0), dns.RcodeSuccess
			if scenario == "address-reused" {
				wantGrants = 1
			}
			if scenario == "blocked-alias" {
				wantRcode = dns.RcodeNameError
			}
			if granted.Load() != wantGrants || resp.Rcode != wantRcode {
				t.Fatalf("unexpected query result: grants=%d response=%s", granted.Load(), resp)
			}
		})
	}
}
