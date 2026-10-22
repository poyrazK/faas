package runtimeadmission

import (
	"github.com/google/uuid"
	"net/netip"
	"testing"
)

func TestEgressPolicyCanonicalCompleteHash(t *testing.T) {
	p := EgressPolicy{AppID: uuid.NewString(), Revision: 9, Allowlist: []netip.Prefix{netip.MustParsePrefix("8.8.8.1/24"), netip.MustParsePrefix("2001:4860::1/64"), netip.MustParsePrefix("8.8.8.0/24")}, Ports: []int{8443, 80, 8443}}
	hash, err := p.Hash()
	if err != nil {
		t.Fatal(err)
	}
	retry := p.Clone()
	retry.Allowlist = []netip.Prefix{netip.MustParsePrefix("2001:4860::/64"), netip.MustParsePrefix("8.8.8.0/24")}
	retry.Ports = []int{80, 8443}
	if got, err := retry.Hash(); err != nil || got != hash {
		t.Fatal("equivalent masked policy changed hash", err)
	}
	for _, change := range []func(*EgressPolicy){func(p *EgressPolicy) { p.Revision++ }, func(p *EgressPolicy) { p.AppID = uuid.NewString() }, func(p *EgressPolicy) { p.Ports = []int{8443} }, func(p *EgressPolicy) { p.Allowlist = p.Allowlist[:1] }} {
		other := retry.Clone()
		change(&other)
		got, err := other.Hash()
		if err != nil || got == hash {
			t.Fatal("complete tuple field not bound", err)
		}
	}
}

func TestEgressPolicyRejectsLossyWireInputs(t *testing.T) {
	identity := Identity{ProtocolVersion: ArtifactProtocolVersion, NodeID: uuid.NewString(), Incarnation: uuid.NewString()}
	p := EgressPolicy{AppID: uuid.NewString(), Revision: 1}
	for _, port := range []int{-1, 0, 25, 465, 587, 65536} {
		other := p.Clone()
		other.Ports = []int{port}
		if _, err := EgressRequest(identity, other); err == nil {
			t.Fatalf("lossy port %d reached wire", port)
		}
	}
	for _, cidr := range []netip.Prefix{{}, netip.MustParsePrefix("::/0")} {
		other := p.Clone()
		other.Allowlist = []netip.Prefix{cidr}
		if _, err := other.Hash(); err == nil {
			t.Fatal("invalid prefix accepted")
		}
	}
	req, err := EgressRequest(identity, p)
	if err != nil {
		t.Fatal(err)
	}
	req.Policy.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	if _, _, err := EgressFromProto(req); err == nil {
		t.Fatal("unknown nested policy accepted")
	}
	identity.ProtocolVersion = ProtocolVersion
	if _, err := EgressRequest(identity, p); err == nil {
		t.Fatal("downlevel native identity accepted")
	}
}
