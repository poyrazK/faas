package edgetopology

// adr: 705

import (
	"fmt"
	"strings"
	"testing"

	"github.com/miekg/dns"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestServedDNSInvalidReviewsCannotStartProviderOrDNSCollection(t *testing.T) {
	for _, mode := range []string{"digest", "parent", "same-parent", "no-parent", "no-child", "duplicate-name", "duplicate-address", "implicit-address", "empty-address", "no-question", "duplicate-question", "external-question", "wildcard", "unsupported-type", "question-bound", "endpoint-bound", "exchange-bound"} {
		t.Run(mode, func(t *testing.T) {
			f := newServedDNSFixture(t, nil)
			switch mode {
			case "digest":
				f.review.ConfigSHA256 = "invalid"
			case "parent":
				f.review.ParentZone = "elsewhere.example"
			case "same-parent":
				f.review.ParentZone = "gregale.dev"
			case "no-parent":
				f.review.Parents = nil
			case "no-child":
				f.review.Authorities = nil
			case "duplicate-name":
				f.review.Authorities = append(f.review.Authorities, f.review.Authorities[0])
			case "duplicate-address":
				f.review.Authorities[0].Addresses = append(f.review.Authorities[0].Addresses, f.review.Authorities[0].Addresses[0])
			case "implicit-address":
				f.review.Authorities[0].Addresses[0] = "localhost:53"
			case "empty-address":
				f.review.Authorities[0].Addresses = nil
			case "no-question":
				f.review.Questions = nil
			case "duplicate-question":
				f.review.Questions = append(f.review.Questions, f.review.Questions[0])
			case "external-question":
				f.review.Questions[0].Name = "outside.example"
			case "wildcard":
				f.review.Questions[0].Name = "*.gregale.dev"
			case "unsupported-type":
				f.review.Questions[0].Type = "HTTPS"
			case "question-bound":
				f.review.Questions = make([]DNSQuestion, api.RuntimeUpgradeServedDNSQuestionLimit+1)
			case "endpoint-bound":
				f.review.Authorities[0].Addresses = make([]string, api.RuntimeUpgradeServedDNSEndpointLimit+1)
			case "exchange-bound":
				f.review.Authorities[0].Addresses = nil
				for i := 0; i < api.RuntimeUpgradeServedDNSEndpointLimit-2; i++ {
					f.review.Authorities[0].Addresses = append(f.review.Authorities[0].Addresses, fmt.Sprintf("127.0.0.1:%d", 10000+i))
				}
				for len(f.review.Questions) < api.RuntimeUpgradeServedDNSQuestionLimit {
					f.review.Questions = append(f.review.Questions, DNSQuestion{Name: fmt.Sprintf("origin%d.gregale.dev", len(f.review.Questions)), Type: "A"})
				}
			}
			before := f.apiCalls.Load()
			if got, err := f.probe.ObserveServed(t.Context(), f.review); err == nil || len(got.Rounds) != 0 || f.apiCalls.Load() != before || f.dnsCalls.Load() != 0 {
				t.Fatal("invalid review began collection", mode, got, err, f.apiCalls.Load(), f.dnsCalls.Load())
			}
		})
	}
}

func TestServedDNSCannotMaskGlueDriftOrAnUninterpretedReferral(t *testing.T) {
	for _, mode := range []string{"glue-drift", "opaque-referral"} {
		t.Run(mode, func(t *testing.T) {
			referrals := 0
			f := newServedDNSFixture(t, func(parent bool, r *dns.Msg) {
				if !parent || r.Question[0].Name != "gregale.dev." {
					return
				}
				referrals++
				if mode == "opaque-referral" {
					r.Ns = append(r.Ns, &dns.DS{Hdr: dnsHeader("gregale.dev", dns.TypeDS), KeyTag: 1, Algorithm: 8, DigestType: 2, Digest: strings.Repeat("a", 64)})
				} else if referrals == 1 {
					r.Extra = []dns.RR{&dns.A{Hdr: dnsHeader("one.ns.cloudflare.com", dns.TypeA), A: []byte{127, 0, 0, 1}}}
				}
			})
			if got, err := f.probe.ObserveServed(t.Context(), f.review); err == nil || len(got.Rounds) != 0 {
				t.Fatal("glue drift or unsupported delegation acquired success", got, err)
			}
		})
	}
}
