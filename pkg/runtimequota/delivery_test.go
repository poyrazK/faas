package runtimequota

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestQuotaDeliveryCanonicalBounds(t *testing.T) {
	// The signed payload is opaque here; signature and operation checks belong
	// to the receiver, after its independent local binding comparison.
	g, _, _, _ := fixture(t)
	d := Delivery{Binding: g.Binding, Authority: Envelope{KeyID: "key", Payload: "opaque", Signature: "signature"}}
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeDelivery(raw); err != nil || got != d {
		t.Fatalf("decode: %+v %v", got, err)
	}
	for _, bad := range [][]byte{
		append(raw, '\n'), append(raw, raw...), []byte(`{}`),
		bytes.Replace(raw, []byte(`"binding":`), []byte(`"binding":{},"binding":`), 1),
		bytes.Replace(raw, []byte(`"key_id":"key"`), []byte(`"key_id":"other","key_id":"key"`), 1),
		[]byte(strings.Repeat("x", api.RegionalRuntimeQuotaDeliveryMaxBytes+1)),
	} {
		if _, err := DecodeDelivery(bad); err == nil {
			t.Fatal("accepted noncanonical/oversized delivery")
		}
	}
	r := DeliveryReceipt{Grant: &d.Authority}
	raw, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeDeliveryReceipt(append(raw, '\n')); err != nil || got.Grant == nil || *got.Grant != d.Authority {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{[]byte(`{"grant":null,"retirement":null}`), append(append(raw, '\n'), '\n'), bytes.Replace(raw, []byte(`"grant":`), []byte(`"grant":null,"grant":`), 1)} {
		if _, err := DecodeDeliveryReceipt(bad); err == nil {
			t.Fatal("accepted ambiguous receipt")
		}
	}
}
