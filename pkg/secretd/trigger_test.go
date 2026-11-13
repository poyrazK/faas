package secretd_test

import (
	"encoding/json"
	"strings"
	"testing"

	"filippo.io/age"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	secretdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/secretd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
)

// Slice 3g: a broker trigger's credentials open by reference with the
// delivery identity, for a trigger of the app, and only for its owner or a
// granted peer.
func TestOpenTriggerConfigByReference(t *testing.T) {
	f := newFixture(t, 0)
	delivery, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	plain := json.RawMessage(`{"brokers":["kafka:9092"],"topic":"orders","sasl":{"mechanism":"PLAIN","username":"u","password":"s3cret"}}`)
	sealed, err := triggerconfig.Seal(api.TriggerKindKafka, plain, []*age.X25519Recipient{delivery.Recipient()})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "s3cret") {
		t.Fatalf("fixture not sealed: %s", sealed)
	}
	f.store.triggers = map[[2]string]fakeTrigger{
		{"app-a", "trig-1"}: {kind: string(api.TriggerKindKafka), config: sealed},
		{"app-b", "trig-2"}: {kind: string(api.TriggerKindKafka), config: sealed},
	}
	open := func(app, trigger string) ([]byte, codes.Code) {
		resp, err := f.server.OpenTriggerConfig(f.ctx, &secretdpb.OpenTriggerConfigRequest{AppId: app, TriggerId: trigger})
		return resp.GetConfigJson(), status.Code(err)
	}
	if _, code := open("app-a", "trig-1"); code != codes.FailedPrecondition {
		t.Fatalf("without a delivery identity = %v, want FailedPrecondition", code)
	}
	f.server.WithDeliveryIdentities([]*age.X25519Identity{delivery})
	opened, code := open("app-a", "trig-1")
	if code != codes.OK || !strings.Contains(string(opened), "s3cret") {
		t.Fatalf("owner = %s, %v", opened, code)
	}
	if f.store.opens[len(f.store.opens)-1] != "n-owner/app-a/owner" {
		t.Fatalf("audit = %v", f.store.opens)
	}
	if _, code := open("app-a", "trig-2"); code != codes.NotFound {
		t.Fatalf("another app's trigger = %v, want NotFound", code)
	}
	if _, code := open("app-b", "trig-2"); code != codes.PermissionDenied {
		t.Fatalf("another node's app = %v, want PermissionDenied", code)
	}
}
