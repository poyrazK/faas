package sealmigrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/triggerconfig"
)

// The contract steps of the compute domains (ADR 20261022111812, step 4):
// each check writes nothing and counts what still opens with the fleet key.

func recipientsOf(ids ...*age.X25519Identity) []*age.X25519Recipient {
	var rs []*age.X25519Recipient
	for _, id := range ids {
		rs = append(rs, id.Recipient())
	}
	return rs
}

func TestCheckEdge(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	seal := func(to ...*age.X25519Identity) []byte {
		b, err := secretbox.SealBytesTo(recipientsOf(to...), api.AppPublicAuthBasicSealNamespace, []byte("user:pass"), api.AppPublicAuthBasicMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	f := &edgeCredentials{rows: map[string]state.EdgeSealedCredential{}}
	for _, c := range []state.EdgeSealedCredential{
		{Kind: state.EdgeSealedCredentialBasicAuth, ID: "a", Sealed: seal(fleet)},
		{Kind: state.EdgeSealedCredentialBasicAuth, ID: "b", Sealed: seal(domain, fleet)},
		{Kind: state.EdgeSealedCredentialBasicAuth, ID: "c", Sealed: seal(domain)},
		// An application standard's destination, still on the fleet key.
		{Kind: state.EdgeSealedCredentialBasicAuth, ID: "d", Sealed: seal(fleet), Managed: true},
	} {
		f.rows[c.Kind+"|"+c.ID] = c
	}
	report, err := sealmigrate.CheckEdge(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Edge, Target: domain.Recipient().String(), Scanned: 4, Current: 1, Pending: 2, Skipped: 1}
	if report != want || f.writes != 0 {
		t.Fatalf("report = %+v (writes %d), want %+v and no writes", report, f.writes, want)
	}
}

func TestCheckControl(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	sealed, err := secretbox.SealBytesTo(recipientsOf(fleet), "internal_svc", []byte("-----BEGIN PRIVATE KEY-----"), 4096)
	if err != nil {
		t.Fatal(err)
	}
	s := &clusterKeyStore{key: state.ClusterSigningKey{KeyID: "k", SealedBlob: sealed}}
	report, err := sealmigrate.CheckControl(context.Background(), s, domain, []*age.X25519Identity{fleet})
	if err != nil || report.Pending != 1 || !bytes.Equal(s.key.SealedBlob, sealed) {
		t.Fatalf("check = %+v, %v; want one pending and the key untouched", report, err)
	}
	if _, err := sealmigrate.MigrateControl(context.Background(), s, domain, []*age.X25519Identity{fleet}); err != nil {
		t.Fatal(err)
	}
	if report, err := sealmigrate.CheckControl(context.Background(), s, domain, []*age.X25519Identity{fleet}); err != nil || report.Pending != 0 || report.Current != 1 {
		t.Fatalf("after the re-seal the check = %+v, %v; want it current", report, err)
	}
	if report, err := sealmigrate.CheckControl(context.Background(), &clusterKeyStore{missing: true}, domain, nil); err != nil || report.Scanned != 0 {
		t.Fatalf("no key: %+v, %v", report, err)
	}
}

// Trigger configs join the delivery re-seal and its check.
func TestDeliveryCoversTriggerConfigs(t *testing.T) {
	domain, fleet := mustIdentity(t), mustIdentity(t)
	plain := json.RawMessage(`{"brokers":["b:9092"],"topic":"t","sasl":{"mechanism":"PLAIN","username":"u","password":"secret-pw"}}`)
	sealedConfig, err := triggerconfig.Seal(api.TriggerKindKafka, plain, recipientsOf(domain, fleet))
	if err != nil {
		t.Fatal(err)
	}
	f := &deliveryValues{rows: map[string]map[string]state.DeliverySealedValue{}}
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedTriggerConfig, ID: "t1", Sealed: sealedConfig, TriggerKind: string(api.TriggerKindKafka)})
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedTriggerConfig, ID: "t2", Sealed: plain, TriggerKind: string(api.TriggerKindKafka)})
	legacy := []*age.X25519Identity{fleet}

	report, err := sealmigrate.CheckDelivery(context.Background(), f, domain, legacy)
	if err != nil || report.Pending != 2 || report.Scanned != 2 {
		t.Fatalf("check = %+v, %v; want both trigger configs pending", report, err)
	}
	if !bytes.Equal(f.rows[state.DeliverySealedTriggerConfig]["t1"].Sealed, sealedConfig) {
		t.Fatal("the check rewrote a trigger config")
	}
	if report, err := sealmigrate.MigrateDelivery(context.Background(), f, domain, legacy); err != nil || report.Resealed != 2 {
		t.Fatalf("migrate = %+v, %v; want both re-sealed", report, err)
	}
	for _, id := range []string{"t1", "t2"} {
		got := f.rows[state.DeliverySealedTriggerConfig][id].Sealed
		if bytes.Contains(got, []byte("secret-pw")) {
			t.Fatalf("%s still holds plaintext", id)
		}
		if _, err := triggerconfig.Open(api.TriggerKindKafka, got, legacy); err == nil {
			t.Fatalf("%s still opens with the fleet key", id)
		}
	}
	if report, err := sealmigrate.CheckDelivery(context.Background(), f, domain, legacy); err != nil || report.Pending != 0 || report.Current != 2 {
		t.Fatalf("after the re-seal the check = %+v, %v; want both current", report, err)
	}
}
