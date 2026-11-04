package sealmigrate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// deliveryValues is an in-memory set of sealed delivery values, keyed by
// kind then id. rotate names one id a customer rotates between the
// re-seal's read and its write.
type deliveryValues struct {
	rows   map[string]map[string]state.DeliverySealedValue
	rotate string
}

func (f *deliveryValues) put(v state.DeliverySealedValue) {
	if f.rows[v.Kind] == nil {
		f.rows[v.Kind] = map[string]state.DeliverySealedValue{}
	}
	f.rows[v.Kind][v.ID] = v
}

func (f *deliveryValues) ListDeliverySealedValues(_ context.Context, kind, afterID string, limit int) ([]state.DeliverySealedValue, error) {
	var out []state.DeliverySealedValue
	for id, v := range f.rows[kind] {
		if id > afterID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *deliveryValues) ResealDeliveryValue(_ context.Context, v state.DeliverySealedValue, sealed []byte, kid string) (bool, error) {
	if v.ID == f.rotate {
		f.rotate = ""
		return false, nil
	}
	row, ok := f.rows[v.Kind][v.ID]
	if !ok || !bytes.Equal(row.Sealed, v.Sealed) {
		return false, nil
	}
	row.Sealed = sealed
	if v.Kind == state.DeliverySealedExecutionPayload {
		row.KID = kid
	}
	f.rows[v.Kind][v.ID] = row
	return true, nil
}

func TestMigrateDelivery(t *testing.T) {
	domain, fleet, stranger := mustIdentity(t), mustIdentity(t), mustIdentity(t)
	recipients := func(ids ...*age.X25519Identity) []*age.X25519Recipient {
		var rs []*age.X25519Recipient
		for _, id := range ids {
			rs = append(rs, id.Recipient())
		}
		return rs
	}
	webhook := func(to ...*age.X25519Identity) []byte {
		b, err := secretbox.SealBytesTo(recipients(to...), api.AppWebhookSecretNamespace, []byte("whsec_0123456789"), api.AppWebhookSecretMaxBytes)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	const conn = "postgres://relay@db.example.com:5432/app?sslmode=verify-full"
	connection := func(sourceID string, to ...*age.X25519Identity) []byte {
		b, err := commit.SealConnectionTo(recipients(to...), sourceID, conn)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	request := api.ResolvedExecutionRequest{Source: "def main(input, context): return input", Input: json.RawMessage(`{"n":1}`)}
	payload := func(to ...*age.X25519Identity) []byte {
		b, err := executionpayload.SealRequestTo(recipients(to...), request)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}

	source, otherSource := uuid.NewString(), uuid.NewString()
	f := &deliveryValues{rows: map[string]map[string]state.DeliverySealedValue{}}
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedAppWebhook, ID: "w-dual", Sealed: webhook(domain, fleet)})
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedAppWebhook, ID: "w-domain", Sealed: webhook(domain)})
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedAppWebhook, ID: "w-stranger", Sealed: webhook(stranger)})
	// More fleet-sealed webhooks than one page holds.
	for i := range 40 {
		f.put(state.DeliverySealedValue{Kind: state.DeliverySealedAppWebhook, ID: fmt.Sprintf("w-fleet-%02d", i), Sealed: webhook(fleet)})
	}
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedCommitConnection, ID: source, Sealed: connection(source, fleet)})
	// A connection copied from another source does not open as this one's.
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedCommitConnection, ID: uuid.NewString(), Sealed: connection(otherSource, fleet)})
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedExecutionPayload, ID: "p-fleet", Sealed: payload(fleet), KID: fleet.Recipient().String()})
	f.put(state.DeliverySealedValue{Kind: state.DeliverySealedExecutionPayload, ID: "p-rotated", Sealed: payload(domain, fleet), KID: fleet.Recipient().String()})
	f.rotate = "p-rotated"

	report, err := sealmigrate.MigrateDelivery(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	want := sealmigrate.MigrationReport{Domain: sealdomain.Delivery, Target: domain.Recipient().String(),
		Scanned: 47, Resealed: 43, Current: 1, Changed: 1, Failed: 2}
	if report != want {
		t.Fatalf("report = %+v, want %+v", report, want)
	}

	for kind, rows := range f.rows {
		for id, v := range rows {
			if id == "w-stranger" || id == "p-rotated" || (kind == state.DeliverySealedCommitConnection && id != source) {
				continue
			}
			if _, _, err := secretbox.OpenBytes(fleet, v.Sealed); err == nil {
				t.Errorf("%s %s still opens with the fleet identity", kind, id)
			}
			if _, _, err := secretbox.OpenBytes(domain, v.Sealed); err != nil {
				t.Errorf("%s %s does not open with the domain identity: %v", kind, id, err)
			}
		}
	}
	if got, err := commit.OpenConnection([]*age.X25519Identity{domain}, source, f.rows[state.DeliverySealedCommitConnection][source].Sealed); err != nil || got != conn {
		t.Fatalf("re-sealed connection = %v; want it bound to its source", err)
	}
	p := f.rows[state.DeliverySealedExecutionPayload]["p-fleet"]
	if p.KID != domain.Recipient().String() {
		t.Fatalf("payload key id = %q, want the domain recipient", p.KID)
	}
	decoded, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{domain}, p.Sealed, p.KID)
	if err != nil || decoded.Source != request.Source {
		t.Fatalf("schedd decode of the re-sealed payload = %+v, %v", decoded, err)
	}

	again, err := sealmigrate.MigrateDelivery(context.Background(), f, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 1 || again.Changed != 0 || again.Current != 44 {
		t.Fatalf("second run = %+v, %v; want only the rotated payload re-sealed", again, err)
	}
}
