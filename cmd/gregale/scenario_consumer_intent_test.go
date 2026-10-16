package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type scenarioConsumerIntentFake struct {
	campaignFaultAPI
	receipt       api.ScenarioTestConsumerReceipt
	lost          bool
	onEnsure      func()
	ensures, keys int
	lookupErr     error
}

type portableConsumerIntentFake struct {
	*campaignFaultHistoryAPI
	cancels  int
	receipts map[string]api.ScenarioTestConsumerReceipt
}

func (f *portableConsumerIntentFake) LookupScenarioTestConsumer(_ context.Context, run, name string, req api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error) {
	intent, err := api.ScenarioTestConsumerIntent(run, name, req)
	if err != nil {
		return api.ScenarioTestConsumerReceipt{}, err
	}
	out := f.receipts[intent]
	if out.Consumer != nil {
		current := f.consumers[out.Consumer.ID]
		current.Name = "Scenario " + name
		out.Consumer = &current
	}
	return out, nil
}

func TestCampaignFaultConsumerIntentPortableRecovery(t *testing.T) {
	// adr: 984 — both committed and uncommitted intents survive portable recovery.
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) { testPortableConsumerIntentRecovery(t, pending) })
	}
}

func testPortableConsumerIntentRecovery(t *testing.T, pending bool) {
	// adr: 984 — recipients resolve lost creation responses without changing frozen ancestor journals.
	f := newCampaignFaultHistoryFixture(t)
	client := &portableConsumerIntentFake{campaignFaultHistoryAPI: newCampaignFaultHistoryAPI(t, f), receipts: map[string]api.ScenarioTestConsumerReceipt{}}
	for index, dir := range f.dirs {
		journals, err := readTestCampaignFaultJournals(dir, "a", f.scenarios["checkout"])
		if err != nil {
			t.Fatal(err)
		}
		j := journals[0]
		c := j.Faults.Consumers["customer"]
		req := api.ScenarioTestConsumerRequest{AppID: c.AppID, ExpiresAt: c.ExpiresAt}
		intent, err := api.ScenarioTestConsumerIntent(j.RunID, "customer", req)
		if err != nil {
			t.Fatal(err)
		}
		live := client.consumers[c.ID]
		live.Name = "Scenario customer"
		client.receipts[intent] = api.ScenarioTestConsumerReceipt{SchemaVersion: 1, IntentSHA256: intent, ExpiresAt: c.ExpiresAt.UTC().Truncate(time.Microsecond), Consumer: &live}
		if pending && index == 0 {
			delete(client.consumers, c.ID)
			receipt := client.receipts[intent]
			receipt.SchemaVersion = 2
			receipt.Consumer = nil
			client.receipts[intent] = receipt
		}
		c.ID = ""
		c.CreationIntentSHA256 = intent
		j.Faults.Consumers["customer"] = c
		mustCampaignFaultJSON(t, filepath.Join(testCampaignJournalDirectory(dir, "a"), j.RunID+".json"), j)
	}
	bundle, err := collectTestCampaignBundle(f.plan, f.cp, f.policy, f.scenarios, f.dirs[2], "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	claims := filepath.Join(t.TempDir(), "claims")
	if err := os.Mkdir(claims, 0700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "recipient")
	if _, err := importTestCampaignBundle(bundle, root, true, claims); err != nil {
		t.Fatal(err)
	}
	resolver, err := loadTestCampaignBundleResolver(filepath.Join(root, "bundle-index.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.root); err != nil {
		t.Fatal(err)
	}
	frozen := campaignFaultFrozenBytes(t, []string{filepath.Join(root, "artifacts")})
	result, err := cleanupTestCampaignFaultHistory(context.Background(), client, f.plan, f.cp, f.scenarios, filepath.Join(root, "work"), resolver, true)
	if err != nil || !result.Complete || client.revokes != 3-client.cancels || client.deletes != 3 || client.namespaces != 3 {
		t.Fatal("portable intent cleanup", result, err)
	}
	for _, observation := range result.Faults {
		if observation.ConsumerIDs["customer"] == "" && observation.Consumers["customer"] != "cancelled" {
			t.Fatal("resolved ID missing")
		}
	}
	if pending && client.cancels != 1 {
		t.Fatal("uncommitted intent not cancelled")
	}
	assertCampaignFaultFrozen(t, frozen, []string{filepath.Join(root, "artifacts")})
}

func (f *scenarioConsumerIntentFake) CreateAPIConsumer(context.Context, string, api.CreateAPIConsumerRequest) (api.APIConsumerResponse, error) {
	return api.APIConsumerResponse{}, errors.New("generic creation forbidden")
}
func (f *scenarioConsumerIntentFake) CreateConsumerKey(context.Context, string, string, api.CreateConsumerKeyRequest) (api.ConsumerKeyResponse, error) {
	f.keys++
	return api.ConsumerKeyResponse{Key: "secret-fixture"}, nil
}
func (f *scenarioConsumerIntentFake) EnsureScenarioTestConsumer(_ context.Context, run, name string, req api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error) {
	f.ensures++
	if f.receipt.Consumer == nil {
		intent, _ := api.ScenarioTestConsumerIntent(run, name, req)
		f.consumer = api.APIConsumerResponse{ID: "11111111-1111-4111-8111-111111111111", AppID: req.AppID, ExternalRef: "gregale-test-" + run + "-" + name, Name: "Scenario " + name, Status: "active"}
		f.receipt = api.ScenarioTestConsumerReceipt{SchemaVersion: 1, IntentSHA256: intent, ExpiresAt: req.ExpiresAt, Consumer: &f.consumer}
	}
	if f.onEnsure != nil {
		fn := f.onEnsure
		f.onEnsure = nil
		fn()
	}
	if f.lost {
		f.lost = false
		return api.ScenarioTestConsumerReceipt{}, errors.New("response lost after commit")
	}
	return f.receipt, nil
}
func (f *scenarioConsumerIntentFake) LookupScenarioTestConsumer(context.Context, string, string, api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error) {
	return f.receipt, f.lookupErr
}

func TestCampaignFaultConsumerIntentRecovery(t *testing.T) {
	// adr: 984 — lost responses and interrupted journal saves recover only the original consumer.
	for _, mode := range []string{"lost-response", "interrupted-save"} {
		t.Run(mode, func(t *testing.T) {
			plan, scenarios, _ := campaignRunnerTestPlan(t, 1)
			scenario := scenarios["checkout"]
			scenario.Consumers = []testConsumer{{Name: "customer"}}
			dir := t.TempDir()
			batch := plan.Batches[0]
			mustCampaignFaultJSON(t, filepath.Join(dir, "checkpoint.json"), testCampaignCheckpoint{SchemaVersion: 1, PlanSHA256: testCampaignJSONHash(plan), Side: batch.Side, Revision: strings.Repeat("a", 40), Batches: map[string]testCampaignCheckpointBatch{batch.ID: {Status: "running"}}})
			journalDir, err := prepareTestCampaignJournal(dir, batch)
			if err != nil {
				t.Fatal(err)
			}
			run := strings.Repeat("a", 32)
			writer, err := newTestCampaignRunJournal(journalDir, "checkout", "warm", run, scenario)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.update("checkout", "owned", "provisioned"); err != nil {
				t.Fatal(err)
			}
			if err := writer.registration("registered"); err != nil {
				t.Fatal(err)
			}
			client := &scenarioConsumerIntentFake{campaignFaultAPI: campaignFaultAPI{members: map[string]string{"checkout": "owned"}}, lost: mode == "lost-response"}
			if mode == "interrupted-save" {
				client.onEnsure = func() { writer.directory = filepath.Join(dir, "missing") }
			}
			if _, _, err := provisionTestConsumers(context.Background(), client, "preview", scenario.Consumers, run, time.Minute, writer); err == nil {
				t.Fatal("interruption was not observed")
			}
			writer.directory = journalDir
			journals, err := readTestCampaignFaultJournals(dir, batch.ID, scenario)
			if err != nil {
				t.Fatal(err)
			}
			saved := journals[0].Faults.Consumers["customer"]
			if saved.ID != "" || saved.CreationIntentSHA256 == "" || client.keys != 0 {
				t.Fatal("intent not saved before creation", saved)
			}
			before := campaignFaultFrozenBytes(t, []string{journalDir})
			obs, err := inspectTestCampaignFaults(context.Background(), client, journals, false, false)
			if err != nil || len(obs) != 1 || obs[0].ConsumerIDs["customer"] != client.consumer.ID || client.revokes != 0 || client.ensures != 1 {
				t.Fatal("read-only recovery", obs, err)
			}
			assertCampaignFaultFrozen(t, before, []string{journalDir})
			original := client.receipt
			client.lookupErr = faultHistoryNotFound()
			if _, err := inspectTestCampaignFaults(context.Background(), client, journals, true, false); err == nil || client.revokes != 0 {
				t.Fatal("unsupported lookup became absence")
			}
			client.lookupErr = nil
			for _, bad := range []string{"missing", "schema", "hash", "expiry", "app", "uuid"} {
				t.Run(bad, func(t *testing.T) {
					client.receipt = original
					copyConsumer := *original.Consumer
					client.receipt.Consumer = &copyConsumer
					switch bad {
					case "missing":
						client.receipt.Consumer = nil
					case "schema":
						client.receipt.SchemaVersion = 0
					case "hash":
						client.receipt.IntentSHA256 = strings.Repeat("b", 64)
					case "expiry":
						client.receipt.ExpiresAt = client.receipt.ExpiresAt.Add(time.Hour)
					case "app":
						copyConsumer.AppID = "replacement"
					case "uuid":
						copyConsumer.ID = "invalid"
					}
					if _, err := inspectTestCampaignFaults(context.Background(), client, journals, true, false); err == nil || client.revokes != 0 {
						t.Fatal("unsafe recovered identity mutated", err)
					}
				})
			}
			client.receipt = original
			writer.journal = journals[0]
			_, ids, err := provisionTestConsumers(context.Background(), client, "preview", scenario.Consumers, run, time.Minute, writer)
			if err != nil || len(ids) != 1 || ids[0] != client.consumer.ID || client.ensures != 2 || client.keys != 1 || !writer.journal.Faults.Consumers["customer"].ExpiresAt.Equal(saved.ExpiresAt) {
				t.Fatal("retry changed intent", ids, err)
			}
			for _, body := range campaignFaultFrozenBytes(t, []string{journalDir}) {
				if bytes.Contains(body, []byte("secret-fixture")) {
					t.Fatal("credential persisted")
				}
			}
			if _, err := inspectTestCampaignFaults(context.Background(), client, journals, true, false); err != nil || client.revokes != 1 {
				t.Fatal("recovered consumer not revoked", err)
			}
			if _, _, err := provisionTestConsumers(context.Background(), client, "preview", scenario.Consumers, run, time.Minute, writer); err == nil {
				t.Fatal("revoked identity was reactivated")
			}
		})
	}
}

func (f *scenarioConsumerIntentFake) RevokeScenarioTestConsumer(ctx context.Context, run, name string, req api.ScenarioTestConsumerRequest, id string) (api.ScenarioTestConsumerReceipt, error) {
	if _, err := f.campaignFaultAPI.RevokeAPIConsumer(ctx, "preview", id); err != nil {
		return api.ScenarioTestConsumerReceipt{}, err
	}
	return f.receipt, nil
}
func (f *portableConsumerIntentFake) RevokeScenarioTestConsumer(ctx context.Context, run, name string, req api.ScenarioTestConsumerRequest, id string) (api.ScenarioTestConsumerReceipt, error) {
	if _, err := f.campaignFaultHistoryAPI.RevokeAPIConsumer(ctx, "preview", id); err != nil {
		return api.ScenarioTestConsumerReceipt{}, err
	}
	return f.LookupScenarioTestConsumer(ctx, run, name, req)
}

func (f *portableConsumerIntentFake) CancelScenarioTestConsumer(_ context.Context, run, name string, req api.ScenarioTestConsumerRequest) (api.ScenarioTestConsumerReceipt, error) {
	intent, _ := api.ScenarioTestConsumerIntent(run, name, req)
	out := f.receipts[intent]
	out.SchemaVersion = 2
	out.Cancelled = true
	out.Consumer = nil
	f.receipts[intent] = out
	f.cancels++
	return out, nil
}

func TestCampaignFaultConsumerCancellationProof(t *testing.T) {
	// adr: 984 — missing receipts are pending, and only a bound cancellation proves safety.
	run := strings.Repeat("a", 32)
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	req := api.ScenarioTestConsumerRequest{AppID: "owned", ExpiresAt: expiry}
	digest, _ := api.ScenarioTestConsumerIntent(run, "customer", req)
	c := testCampaignFaultConsumer{AppID: "owned", AppSlug: "preview", ExternalRef: "gregale-test-" + run + "-customer", ExpiresAt: expiry, CreationIntentSHA256: digest}
	client := &scenarioConsumerIntentFake{receipt: api.ScenarioTestConsumerReceipt{SchemaVersion: 2, IntentSHA256: digest, ExpiresAt: expiry}}
	if _, err := readTestCampaignConsumer(context.Background(), client, run, "customer", c); !errors.Is(err, errTestConsumerPending) {
		t.Fatal("pending became absence", err)
	}
	client.receipt.Cancelled = true
	if _, err := readTestCampaignConsumer(context.Background(), client, run, "customer", c); !errors.Is(err, errTestConsumerCancelled) {
		t.Fatal("valid cancellation refused", err)
	}
	for _, bad := range []string{"old-schema", "digest", "expiry"} {
		original := client.receipt
		switch bad {
		case "old-schema":
			client.receipt.SchemaVersion = 1
		case "digest":
			client.receipt.IntentSHA256 = strings.Repeat("b", 64)
		case "expiry":
			client.receipt.ExpiresAt = expiry.Add(time.Hour)
		}
		if _, err := readTestCampaignConsumer(context.Background(), client, run, "customer", c); errors.Is(err, errTestConsumerCancelled) || errors.Is(err, errTestConsumerPending) || err == nil {
			t.Fatal("untrusted proof", bad, err)
		}
		client.receipt = original
	}
}
