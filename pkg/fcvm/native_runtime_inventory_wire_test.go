package fcvm

// adr: 777
import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimequota"
)

func TestNativeInventoryWireRequiresCanonicalRequestBoundSample(t *testing.T) {
	sample := NativeRuntimeInventorySample{Version: 1, Host: runtimequota.HostEpoch{NodeID: uuid.NewString(), HostEpochID: uuid.NewString()}, Challenge: uuid.NewString(), Inventory: NativeRuntimeInventory{KernelBootID: uuid.NewString()}}
	raw, err := EncodeNativeRuntimeInventorySample(sample)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeNativeRuntimeInventorySample(raw)
	if err != nil || got.Host != sample.Host || got.Challenge != sample.Challenge {
		t.Fatal(err)
	}
	for name, bad := range map[string][]byte{
		"duplicate":        bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":1,"Version":1`), 1),
		"unknown":          append([]byte(`{"Unknown":1,`), raw[1:]...),
		"null-host":        bytes.Replace(raw, []byte(`"Host":{`), []byte(`"Host":null,"Host":{`), 1),
		"version":          bytes.Replace(raw, []byte(`"Version":1`), []byte(`"Version":2`), 1),
		"alternate-casing": bytes.Replace(raw, []byte(`"Challenge"`), []byte(`"challenge"`), 1),
		"zero-challenge":   []byte(strings.Replace(string(raw), sample.Challenge, uuid.Nil.String(), 1)),
		"trailing":         append(append([]byte(nil), raw...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeNativeRuntimeInventorySample(bad); err == nil {
				t.Fatal("ambiguous sample accepted")
			}
		})
	}
}
