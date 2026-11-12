package triggerconfig

import (
	"bytes"
	"encoding/json"
	"testing"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261022111812: the delivery re-seal moves a trigger config's
// credentials onto the domain alone. Dual-sealed and partly moved configs
// still count as legacy; plaintext credentials are sealed; configs without
// credentials are current.
func TestReseal(t *testing.T) {
	newID := func() *age.X25519Identity {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	domain, fleet, stranger := newID(), newID(), newID()
	plain := json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","sasl":{"mechanism":"PLAIN","username":"svc","password":"sasl-secret"},"tls":{"client_cert":"cert","client_key":"PRIVATE KEY"}}`)
	seal := func(raw json.RawMessage, to ...*age.X25519Identity) json.RawMessage {
		var rs []*age.X25519Recipient
		for _, id := range to {
			rs = append(rs, id.Recipient())
		}
		out, err := Seal(api.TriggerKindKafka, raw, rs)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	// Only the TLS key sealed to the domain; the SASL password to the fleet.
	mixed := func() json.RawMessage {
		domainOnly := seal(plain, domain)
		fleetOnly := seal(plain, fleet)
		var d, f map[string]json.RawMessage
		if err := json.Unmarshal(domainOnly, &d); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(fleetOnly, &f); err != nil {
			t.Fatal(err)
		}
		d["sasl"] = f["sasl"]
		out, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}()
	legacy := []*age.X25519Identity{fleet}
	for _, tc := range []struct {
		name string
		kind api.TriggerKind
		raw  json.RawMessage
		want ResealStatus
	}{
		{"no credentials", api.TriggerKindKafka, json.RawMessage(`{"brokers":["b:9092"],"topic":"orders"}`), ResealCurrent},
		{"kind without credentials", api.TriggerKindQueue, json.RawMessage(`{"queue":"q"}`), ResealCurrent},
		{"domain only", api.TriggerKindKafka, seal(plain, domain), ResealCurrent},
		{"fleet only", api.TriggerKindKafka, seal(plain, fleet), ResealWrite},
		{"dual", api.TriggerKindKafka, seal(plain, domain, fleet), ResealWrite},
		{"mixed", api.TriggerKindKafka, mixed, ResealWrite},
		{"plaintext", api.TriggerKindKafka, plain, ResealWrite},
		{"nobody opens it", api.TriggerKindKafka, seal(plain, stranger), ResealFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, status, err := Reseal(tc.kind, tc.raw, domain, legacy)
			if err != nil {
				t.Fatal(err)
			}
			if status != tc.want {
				t.Fatalf("status = %d, want %d", status, tc.want)
			}
			if status != ResealWrite {
				if out != nil {
					t.Fatal("only a write returns a config")
				}
				return
			}
			if bytes.Contains(out, []byte("sasl-secret")) || bytes.Contains(out, []byte("PRIVATE KEY")) {
				t.Fatalf("re-sealed config holds plaintext: %s", out)
			}
			if _, err := Open(tc.kind, out, legacy); err == nil {
				t.Fatal("the fleet identity still opens the re-sealed config")
			}
			opened, err := Open(tc.kind, out, []*age.X25519Identity{domain})
			if err != nil {
				t.Fatalf("the domain does not open the re-sealed config: %v", err)
			}
			if !bytes.Contains(opened, []byte("sasl-secret")) || !bytes.Contains(opened, []byte("PRIVATE KEY")) {
				t.Fatalf("credentials lost: %s", opened)
			}
			if _, again, _ := Reseal(tc.kind, out, domain, legacy); again != ResealCurrent {
				t.Fatalf("a second pass = %d, want current", again)
			}
		})
	}
}
