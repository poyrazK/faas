package sealmigrate_test

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/executionpayload"
	"github.com/onebox-faas/faas/pkg/sealdomain/sealmigrate"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPostgresMigrateDelivery runs the delivery re-seal against PostgreSQL
// (ADR 20261022111812): an app webhook's signing secret, a commit source's
// connection and a queued execution's payload move to the domain alone, the
// payload under the domain's key id, and a second run finds them current.
func TestPostgresMigrateDelivery(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	fleet, domain := mustIdentity(t), mustIdentity(t)
	toFleet := []*age.X25519Recipient{fleet.Recipient()}
	suffix := strconv.FormatInt(rand.Int64N(1<<40), 36)
	account, err := store.CreateAccount(ctx, "delivery-domain-"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "delivery-domain-" + suffix, Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}

	secret, err := secretbox.SealBytesTo(toFleet, api.AppWebhookSecretNamespace, []byte("whsec-delivery"), api.AppWebhookSecretMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{AppID: app.ID, AccountID: account.ID, TargetURL: "https://hooks.example.com/" + suffix, SecretSealed: secret, Enabled: true, EventFilter: []string{"deploy.succeeded"}})
	if err != nil {
		t.Fatal(err)
	}

	source, err := store.CreateCommitSource(ctx, state.CommitSource{AccountID: account.ID, AppID: app.ID, Name: "orders-" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	const conn = "postgres://relay@db.example.com:5432/app?sslmode=verify-full"
	connection, err := commit.SealConnectionTo(toFleet, source.ID, conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetCommitSourceConnection(ctx, account.ID, source.ID, connection); err != nil {
		t.Fatal(err)
	}

	request := api.CreateExecutionRequest{Runtime: api.ExecutionRuntimeNode22, Source: "export default async function main(input) { return input }", Input: []byte(`{"n":1}`)}
	resolved, problem := request.Resolve(api.PlanPro)
	if problem != nil {
		t.Fatal(problem)
	}
	payload, err := executionpayload.SealRequestTo(toFleet, resolved)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	execution, err := store.CreateExecution(ctx, state.CreateExecutionParams{AccountID: account.ID, Request: resolved,
		SourceBytes: len(request.Source), InputBytes: len(request.Input), AdmittedAt: now,
		DeadlineAt: now.Add(time.Duration(resolved.Limits.TimeoutMS) * time.Millisecond), SealedPayload: payload, PayloadKID: fleet.Recipient().String()})
	if err != nil {
		t.Fatal(err)
	}

	report, err := sealmigrate.MigrateDelivery(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned < 3 || report.Resealed < 3 {
		t.Fatalf("report = %+v, want the webhook secret, the connection and the payload re-sealed", report)
	}
	got := map[string]state.DeliverySealedValue{}
	for kind, id := range map[string]string{state.DeliverySealedAppWebhook: hook.ID, state.DeliverySealedCommitConnection: source.ID, state.DeliverySealedExecutionPayload: execution.ID} {
		v := deliveryValue(t, store, kind, id)
		if _, _, err := secretbox.OpenBytes(fleet, v.Sealed); err == nil {
			t.Errorf("%s still opens with the fleet identity", kind)
		}
		got[kind] = v
	}
	if ns, plain, err := secretbox.OpenBytes(domain, got[state.DeliverySealedAppWebhook].Sealed); err != nil || ns != api.AppWebhookSecretNamespace || string(plain) != "whsec-delivery" {
		t.Fatalf("webhook secret domain open = %q, %v", ns, err)
	}
	if opened, err := commit.OpenConnection([]*age.X25519Identity{domain}, source.ID, got[state.DeliverySealedCommitConnection].Sealed); err != nil || opened != conn {
		t.Fatalf("connection domain open: %v", err)
	}
	p := got[state.DeliverySealedExecutionPayload]
	if p.KID != domain.Recipient().String() {
		t.Fatalf("payload key id = %q, want the domain recipient", p.KID)
	}
	if decoded, err := executionpayload.DecodeRequest(ctx, []*age.X25519Identity{domain}, p.Sealed, p.KID); err != nil || decoded.Source != request.Source || !json.Valid(decoded.Input) {
		t.Fatalf("schedd decode of the re-sealed payload: %v", err)
	}

	again, err := sealmigrate.MigrateDelivery(ctx, store, domain, []*age.X25519Identity{fleet})
	if err != nil || again.Resealed != 0 {
		t.Fatalf("second run = %+v, %v; want nothing re-sealed", again, err)
	}
}

func deliveryValue(t *testing.T, store *state.PgStore, kind, id string) state.DeliverySealedValue {
	t.Helper()
	after := ""
	for {
		page, err := store.ListDeliverySealedValues(context.Background(), kind, after, 500)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page {
			if v.ID == id {
				return v
			}
			after = v.ID
		}
		if len(page) < 500 {
			t.Fatalf("%s %s not listed", kind, id)
		}
	}
}
