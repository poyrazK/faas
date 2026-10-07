package runtimeadmission

import (
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestPromotionRequiresFreshPurposeAndExactHistoricalLease(t *testing.T) {
	now := time.Now()
	old := now.Add(-time.Hour)
	parent := Receipt{Binding: validBinding(old), NativeInputHash: strings.Repeat("d", 64), Netns: "native-promotion", HostIP: "10.100.0.8", LeaseUID: 20008, Paused: true, CompletedAtUnixNano: old.UnixNano()}
	p := Promotion{Parent: parent, Binding: parent.Binding}
	p.Binding.Token, p.Binding.IssuedAtUnixNano, p.Binding.ExpiresAtUnixNano = uuid.NewString(), now.UnixNano(), now.Add(time.Minute).UnixNano()
	p.Binding.PayloadHash, _ = HashPromotionPayload(p.ToProto())
	if err := p.Validate(now); err != nil {
		t.Fatalf("healthy old parent expired promotion: %v", err)
	}
	r := parent
	r.Binding, r.Paused, r.CompletedAtUnixNano = p.Binding, false, now.UnixNano()
	if err := p.CheckReceipt(r, now); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Promotion){
		func(p *Promotion) { p.Binding.Token = p.Parent.Binding.Token },
		func(p *Promotion) { p.Binding.EgressRevision++ },
		func(p *Promotion) { p.Binding.Incarnation = uuid.NewString() },
		func(p *Promotion) { p.Parent.Netns = "another-lease" },
		func(p *Promotion) { p.Parent.Paused = false },
		func(p *Promotion) { p.Binding.ExpiresAtUnixNano = now.UnixNano() },
	} {
		bad := p
		change(&bad)
		if bad.Validate(now) == nil {
			t.Fatal("changed promotion accepted")
		}
	}
	for _, change := range []func(*Receipt){
		func(r *Receipt) { r.NativeInputHash = strings.Repeat("e", 64) },
		func(r *Receipt) { r.Netns = "another" },
		func(r *Receipt) { r.LeaseUID++ },
		func(r *Receipt) { r.Paused = true },
	} {
		bad := r
		change(&bad)
		if p.CheckReceipt(bad, now) == nil {
			t.Fatal("changed native receipt accepted")
		}
	}
	wire := p.ToProto()
	wire.Parent.Binding.ProtoReflect().SetUnknown([]byte{0x98, 0x06, 0x01})
	if _, err := HashPromotionPayload(wire); err == nil {
		t.Fatal("ignored unknown parent field")
	}
}
