package hosting

import (
	"context"
	"testing"
)

func TestDomainNormalizationAndIsolation(t *testing.T) {
	for _, host := range []string{"example.com", "co.uk", "localhost", "127.0.0.1", "*.other.example", "evil.gregale.dev", "portal.hosting.example", "bad..example.com", "user@example.com", "a.example.com/path", "a.example.com:443", "_x.example.com", "a.workers.dev", "a.example.com."} {
		if _, err := NormalizeDomain(host, []string{"Gregale.dev", "hosting.example"}); err == nil {
			t.Errorf("accepted %q", host)
		}
	}
	for input, want := range map[string]string{" Portal.Example.com ": "portal.example.com", "www.bücher.example": "www.xn--bcher-kva.example", "www.customer.co.uk": "www.customer.co.uk"} {
		got, err := NormalizeDomain(input, nil)
		if err != nil || got != want {
			t.Errorf("%q: %q %v", input, got, err)
		}
	}
}

type domainTestDNS struct {
	txt   []string
	cname string
	err   error
}

func (d domainTestDNS) LookupTXT(context.Context, string) ([]string, error) { return d.txt, d.err }
func (d domainTestDNS) LookupCNAME(context.Context, string) (string, error) { return d.cname, d.err }
func TestDomainDNSRequiresExactClaimAndTarget(t *testing.T) {
	for _, tc := range []struct {
		txt   []string
		cname string
		want  bool
	}{
		{[]string{"gregale-hosting=token"}, "customers.hosting.example.", true},
		{[]string{"gregale-hosting=other"}, "customers.hosting.example.", false},
		{[]string{"gregale-hosting=token"}, "customers.hosting.example.evil.", false},
		{nil, "customers.hosting.example.", false},
	} {
		if got := VerifyDomainDNS(t.Context(), domainTestDNS{txt: tc.txt, cname: tc.cname}, "portal.customer.example", "token", "customers.hosting.example"); got != tc.want {
			t.Fatalf("DNS proof: %v", got)
		}
	}
}
